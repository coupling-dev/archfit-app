// Package firestore is the persistent store.Store driver: Cloud Firestore in Native mode over its REST API v1.
package firestore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	// DefaultDatabase is the database ID used when Config.DatabaseID is empty.
	DefaultDatabase = "(default)"

	apiBase    = "https://firestore.googleapis.com/v1/"
	collection = "kv"

	fieldTenant = "tenant"
	fieldValue  = "value"

	// The input limits of internal/store/module.md.
	maxKeyLen   = 512
	maxValueLen = 64 << 10
	maxSwaps    = 10

	maxResponseBytes = 8 << 20
	// deleteBatch is the number of documents DeleteTenant reads and deletes in one commit. The deleted documents
	// count toward the 10 MiB limit of one commit: 100 values of 64 KiB, with their index entries, stay under it.
	deleteBatch = 100

	// maxAttempts bounds the read-compare-commit loop of CompareAndSwap, CompareAndSwapAll and Add on a conflict. A local lock
	// serializes the callers of one process, so only other instances conflict.
	maxAttempts = 10
	retryBase   = 10 * time.Millisecond
	retryCap    = 500 * time.Millisecond

	// lockStripes bounds the local locks; two keys on one stripe only wait for each other.
	lockStripes = 64
	// emulatorToken is the documented admin bypass of the Firestore emulator; it is not a credential.
	emulatorToken = "owner"
)

var (
	projectIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	databaseIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,61}[a-z0-9]$`)

	errCorrupt = errors.New("store: firestore: stored document is not a store value")
)

// TokenSource returns an OAuth access token for the Firestore API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Config selects the database.
type Config struct {
	ProjectID  string // Google Cloud project ID
	DatabaseID string // empty selects DefaultDatabase
	// EmulatorHost is host:port of a Firestore emulator. When set, the driver uses plain HTTP and no credentials.
	EmulatorHost string
}

// New returns a Store over the database in c. It validates c and contacts no server.
// tokens may be nil only in emulator mode.
func New(c Config, tokens TokenSource, hc *http.Client) (store.Store, error) {
	if c.DatabaseID == "" {
		c.DatabaseID = DefaultDatabase
	}
	switch {
	case !projectIDPattern.MatchString(c.ProjectID):
		return nil, errors.New("store: firestore: invalid project ID")
	case c.DatabaseID != DefaultDatabase && !databaseIDPattern.MatchString(c.DatabaseID):
		return nil, errors.New("store: firestore: invalid database ID")
	case strings.ContainsAny(c.EmulatorHost, "/?#@ "):
		return nil, errors.New("store: firestore: the emulator host must be host:port")
	case c.EmulatorHost == "" && tokens == nil:
		return nil, errors.New("store: firestore: a token source is required outside the emulator")
	case hc == nil:
		return nil, errors.New("store: firestore: an HTTP client is required")
	}
	base := apiBase
	if c.EmulatorHost != "" {
		base = "http://" + c.EmulatorHost + "/v1/"
		tokens = staticToken(emulatorToken)
	}
	d := &driver{
		documents: "projects/" + c.ProjectID + "/databases/" + c.DatabaseID + "/documents",
		base:      base,
		tokens:    tokens,
		hc:        hc,
		seed:      maphash.MakeSeed(),
		wait:      sleep,
	}
	for i := range d.locks {
		d.locks[i] = make(chan struct{}, 1)
	}
	return d, nil
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

type driver struct {
	documents string // <database>/documents
	base      string
	tokens    TokenSource
	hc        *http.Client

	seed  maphash.Seed
	locks [lockStripes]chan struct{}                 // one-slot locks that a waiting caller can leave when its context ends
	wait  func(context.Context, time.Duration) error // the retry pause; tests replace it
}

// docName maps a key to one document. The document ID is "<tenant>-<hex key>": a key may hold "/" and any
// byte except NUL, which a document ID cannot; hex keeps the ID valid and under 1,500 bytes.
func (d *driver) docName(t store.Tenant, key string) string {
	return d.documents + "/" + collection + "/" + strconv.FormatInt(int64(t), 10) + "-" + hex.EncodeToString([]byte(key))
}

// lock serializes the read-compare-commit operations on the documents inside this process. It only lowers
// contention: atomicity across instances comes from the commit preconditions. It takes each stripe once and in
// ascending order, so two callers never wait for each other in a cycle, and two names on one stripe never
// wait for themselves. A caller whose context ends while it waits leaves with the context error.
func (d *driver) lock(ctx context.Context, names ...string) (func(), error) {
	stripes := make([]uint64, 0, len(names))
	for _, name := range names {
		stripes = append(stripes, maphash.String(d.seed, name)%lockStripes)
	}
	slices.Sort(stripes)
	stripes = slices.Compact(stripes)
	held := 0
	unlock := func() {
		for _, i := range stripes[:held] {
			<-d.locks[i]
		}
	}
	for _, i := range stripes {
		select {
		case d.locks[i] <- struct{}{}:
			held++
		case <-ctx.Done():
			unlock()
			return nil, ctx.Err()
		}
	}
	return unlock, nil
}

func (d *driver) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateKey("get", t, key); err != nil {
		return nil, err
	}
	doc, err := d.read(ctx, d.docName(t, key))
	switch {
	case err != nil:
		return nil, err
	case !doc.found:
		return nil, store.ErrNotFound
	}
	return doc.value, nil
}

func (d *driver) Put(ctx context.Context, t store.Tenant, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey("put", t, key); err != nil {
		return err
	}
	if len(value) > maxValueLen {
		return opError("put", t, key, "value longer than "+strconv.Itoa(maxValueLen)+" bytes")
	}
	return d.commit(ctx, d.update(t, key, value, nil))
}

func (d *driver) Delete(ctx context.Context, t store.Tenant, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey("delete", t, key); err != nil {
		return err
	}
	name := d.docName(t, key)
	return d.commit(ctx, write{Delete: name})
}

func (d *driver) CompareAndSwap(ctx context.Context, t store.Tenant, key string, old, value []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateKey("compare and swap", t, key); err != nil {
		return false, err
	}
	if len(value) > maxValueLen {
		return false, opError("compare and swap", t, key, "value longer than "+strconv.Itoa(maxValueLen)+" bytes")
	}
	name := d.docName(t, key)
	swapped := false
	err := d.readModifyWrite(ctx, []string{name}, func(cur []stored) ([]write, error) {
		if !cur[0].holds(old) {
			swapped = false
			return nil, nil
		}
		swapped = true
		if value == nil && !cur[0].found {
			return nil, nil // nothing to delete; the comparison held at the read
		}
		return []write{d.swapWrite(t, key, value, cur[0])}, nil
	})
	if err != nil {
		return false, err
	}
	return swapped, nil
}

func (d *driver) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateSwaps(t, swaps); err != nil {
		return false, err
	}
	names := make([]string, len(swaps))
	for i, s := range swaps {
		names[i] = d.docName(t, s.Key)
	}
	swapped := false
	err := d.readModifyWrite(ctx, names, func(cur []stored) ([]write, error) {
		swapped = false
		for i, s := range swaps {
			if !cur[i].holds(s.Old) {
				return nil, nil
			}
		}
		swapped = true
		// Every swap is a write, also a missing key that stays missing: its delete under exists=false keeps
		// the key guarded until the commit.
		writes := make([]write, len(swaps))
		for i, s := range swaps {
			writes[i] = d.swapWrite(t, s.Key, s.Value, cur[i])
		}
		return writes, nil
	})
	if err != nil {
		return false, err
	}
	return swapped, nil
}

// swapWrite writes value at key, or deletes the key for a nil value, under a precondition on cur.
func (d *driver) swapWrite(t store.Tenant, key string, value []byte, cur stored) write {
	if value == nil {
		return write{Delete: d.docName(t, key), CurrentDocument: precondition(cur)}
	}
	return d.update(t, key, value, precondition(cur))
}

func (d *driver) Add(ctx context.Context, t store.Tenant, key string, delta int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateKey("add", t, key); err != nil {
		return 0, err
	}
	var next int64
	err := d.readModifyWrite(ctx, []string{d.docName(t, key)}, func(cur []stored) ([]write, error) {
		var n int64
		if cur[0].found {
			var err error
			// The strconv error quotes its input, so it is dropped: errors never carry a value.
			if n, err = strconv.ParseInt(string(cur[0].value), 10, 64); err != nil || strconv.FormatInt(n, 10) != string(cur[0].value) {
				return nil, opError("add", t, key, "value is not a counter")
			}
		}
		if (delta > 0 && n > math.MaxInt64-delta) || (delta < 0 && n < math.MinInt64-delta) {
			return nil, opError("add", t, key, "counter overflow")
		}
		next = n + delta
		return []write{d.update(t, key, strconv.AppendInt(nil, next, 10), precondition(cur[0]))}, nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// readModifyWrite reads the documents, lets decide compute the writes, and commits them in one commit under
// preconditions on what was read. decide gets the documents in the order of names. On a conflict, which is a
// definite rejection, it reads again. Any other commit failure is returned as is: the writes may have landed,
// so a retry could report a swap it made as a failed comparison.
func (d *driver) readModifyWrite(ctx context.Context, names []string, decide func([]stored) ([]write, error)) error {
	unlock, err := d.lock(ctx, names...)
	if err != nil {
		return err
	}
	defer unlock()
	for attempt := 1; ; attempt++ {
		cur, err := d.readAll(ctx, names)
		if err != nil {
			return err
		}
		writes, err := decide(cur)
		if err != nil || len(writes) == 0 {
			return err
		}
		err = d.commit(ctx, writes...)
		var apiErr *apiError
		if !errors.As(err, &apiErr) || !apiErr.conflict() {
			return err
		}
		if attempt == maxAttempts {
			return fmt.Errorf("store: firestore: still in conflict after %d attempts: %w", attempt, err)
		}
		if err := d.wait(ctx, backoff(attempt)); err != nil {
			return err
		}
	}
}

func backoff(attempt int) time.Duration {
	d := min(retryBase<<(attempt-1), retryCap)
	return d/2 + rand.N(d/2+1)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (d *driver) DeleteTenant(ctx context.Context, t store.Tenant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t < 0 {
		return opError("delete tenant", t, "", "negative tenant")
	}
	if t == store.SystemTenant {
		return opError("delete tenant", t, "", "the system tenant cannot be deleted")
	}
	// Each pass reads the names of up to deleteBatch documents of the tenant and deletes them in one commit,
	// until none is left.
	for {
		docs, err := d.query(ctx, structuredQuery{
			Select: &projection{Fields: []fieldRef{{FieldPath: "__name__"}}},
			Where:  tenantFilter("EQUAL", int64(t)),
			Limit:  deleteBatch,
		})
		if err != nil || len(docs) == 0 {
			return err
		}
		writes := make([]write, len(docs))
		for i, doc := range docs {
			writes[i] = write{Delete: doc.Name}
		}
		if err := d.commit(ctx, writes...); err != nil {
			return err
		}
	}
}

func (d *driver) Tenants(ctx context.Context) ([]store.Tenant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One indexed query per tenant: the next tenant above the last one found. SystemTenant (0) is never above 0.
	tenants := []store.Tenant{}
	for last := int64(store.SystemTenant); ; {
		docs, err := d.query(ctx, structuredQuery{
			Select:  &projection{Fields: []fieldRef{{FieldPath: fieldTenant}}},
			Where:   tenantFilter("GREATER_THAN", last),
			OrderBy: []order{{Field: fieldRef{FieldPath: fieldTenant}, Direction: "ASCENDING"}},
			Limit:   1,
		})
		if err != nil {
			return nil, err
		}
		if len(docs) == 0 {
			return tenants, nil
		}
		id, err := docs[0].Fields[fieldTenant].integer()
		if err != nil || id <= last {
			return nil, errCorrupt
		}
		tenants = append(tenants, store.Tenant(id))
		last = id
	}
}

// update is a write that creates or replaces the whole document of key.
func (d *driver) update(t store.Tenant, key string, value []byte, pre *precond) write {
	return write{
		Update: &document{
			Name: d.docName(t, key),
			Fields: map[string]fsValue{
				fieldTenant: integerValue(int64(t)),
				fieldValue:  bytesValue(value),
			},
		},
		CurrentDocument: pre,
	}
}

// precondition requires the document to be exactly as read: missing, or unchanged since its update time.
func precondition(cur stored) *precond {
	if !cur.found {
		f := false
		return &precond{Exists: &f}
	}
	return &precond{UpdateTime: cur.updateTime}
}

// stored is a decoded document, or a missing one.
type stored struct {
	found      bool
	value      []byte // non-nil, also when empty, if found
	updateTime string // opaque; only passed back in a precondition
}

// holds reports whether the document holds exactly old, with the rules of CompareAndSwap: a nil old matches
// only a missing document.
func (s stored) holds(old []byte) bool {
	return s.found == (old != nil) && bytes.Equal(s.value, old)
}

func (d *driver) read(ctx context.Context, name string) (stored, error) {
	docs, err := d.readAll(ctx, []string{name})
	if err != nil {
		return stored{}, err
	}
	return docs[0], nil
}

// readAll reads the distinct documents names in one batchGet and returns them in the order of names.
// It uses batchGet rather than a plain GET: batchGet reports a missing document as "missing", so a NOT_FOUND
// status always means a missing database or project and stays an error, never a missing key. The answer
// order is not defined, so each document is matched by name, and each must be answered exactly once.
func (d *driver) readAll(ctx context.Context, names []string) ([]stored, error) {
	items, err := d.stream(ctx, d.documents+":batchGet", batchGetRequest{Documents: names})
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	docs := make([]stored, len(names))
	answered := make([]bool, len(names))
	for _, it := range items {
		name := it.Missing
		if it.Found != nil {
			name = it.Found.Name
		}
		if name == "" {
			continue // an element without a document only carries the read time
		}
		i, ok := index[name]
		if !ok || answered[i] || (it.Found != nil && it.Missing != "") {
			return nil, errors.New("store: firestore: batchGet answered for an unexpected document")
		}
		answered[i] = true
		if it.Found != nil {
			if docs[i], err = decode(it.Found); err != nil {
				return nil, err
			}
		}
	}
	if slices.Contains(answered, false) {
		return nil, errors.New("store: firestore: batchGet did not answer for every document")
	}
	return docs, nil
}

func decode(doc *document) (stored, error) {
	v, ok := doc.Fields[fieldValue]
	if !ok || v.BytesValue == nil || doc.UpdateTime == "" {
		return stored{}, errCorrupt
	}
	b, err := base64.StdEncoding.DecodeString(*v.BytesValue)
	if err != nil {
		return stored{}, errCorrupt
	}
	if b == nil {
		b = []byte{}
	}
	return stored{found: true, value: b, updateTime: doc.UpdateTime}, nil
}

func (d *driver) commit(ctx context.Context, writes ...write) error {
	return d.call(ctx, d.documents+":commit", commitRequest{Writes: writes}, nil)
}

func (d *driver) query(ctx context.Context, q structuredQuery) ([]document, error) {
	q.From = []collectionSelector{{CollectionID: collection}}
	items, err := d.stream(ctx, d.documents+":runQuery", runQueryRequest{StructuredQuery: q})
	if err != nil {
		return nil, err
	}
	docs := make([]document, 0, len(items))
	for _, it := range items {
		if it.Document != nil { // an element without a document only carries the read time
			docs = append(docs, *it.Document)
		}
	}
	return docs, nil
}

// stream calls a streaming method. Its answer is a JSON array, and an error after the first element arrives
// as a last element with an "error" member and a 200 status. Such an answer is an error as a whole: a partial
// result would let DeleteTenant or Tenants stop early and report success.
func (d *driver) stream(ctx context.Context, path string, in any) ([]streamItem, error) {
	var items []streamItem
	if err := d.call(ctx, path, in, &items); err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Error != nil {
			return nil, &apiError{HTTPStatus: http.StatusOK, Status: safeToken(it.Error.Status)}
		}
	}
	return items, nil
}

// call sends one request. Errors carry the HTTP and RPC status only: a Firestore message can quote a
// document name, which holds the key.
func (d *driver) call(ctx context.Context, path string, in, out any) error {
	token, err := d.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("store: firestore: access token: %w", err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("store: firestore: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("store: firestore: building request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("store: firestore: request failed: %w", ctxErr)
		}
		// The url.Error quotes the URL, which holds the key, so only the cause is kept.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("store: firestore: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("store: firestore: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Status string `json:"status"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{HTTPStatus: resp.StatusCode, Status: safeToken(e.Error.Status)}
	}
	if len(raw) > maxResponseBytes {
		return errors.New("store: firestore: response larger than 8 MiB")
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("store: firestore: response is not valid JSON")
	}
	return nil
}

const (
	statusNotFound           = "NOT_FOUND"
	statusAborted            = "ABORTED"
	statusFailedPrecondition = "FAILED_PRECONDITION"
	statusAlreadyExists      = "ALREADY_EXISTS"
)

// apiError is a response other than 200.
type apiError struct {
	HTTPStatus int
	Status     string // the google.rpc status name, such as FAILED_PRECONDITION
}

func (e *apiError) Error() string {
	return fmt.Sprintf("store: firestore: status %d (%s)", e.HTTPStatus, e.Status)
}

// conflict reports a commit that Firestore rejected because the document changed after the read, or because
// of contention. Nothing was written.
func (e *apiError) conflict() bool {
	switch e.Status {
	case statusFailedPrecondition, statusAlreadyExists, statusNotFound, statusAborted:
		return true
	}
	return false
}

// safeToken passes an RPC status name such as FAILED_PRECONDITION and nothing that could be prose.
func safeToken(s string) string {
	if s == "" || len(s) > 64 {
		return "UNKNOWN"
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && r != '_' {
			return "UNKNOWN"
		}
	}
	return s
}

// opError matches the validation errors of internal/store: tenant and key length only, never the key or the value.
func opError(op string, t store.Tenant, key, problem string) error {
	return fmt.Errorf("store: %s: tenant %d, key length %d: %s", op, t, len(key), problem)
}

// validateSwaps checks the input of CompareAndSwapAll like internal/store does.
func validateSwaps(t store.Tenant, swaps []store.Swap) error {
	const op = "compare and swap all"
	if t < 0 {
		return opError(op, t, "", "negative tenant")
	}
	if len(swaps) == 0 || len(swaps) > maxSwaps {
		return fmt.Errorf("store: %s: tenant %d: %d swaps, want 1 to %d", op, t, len(swaps), maxSwaps)
	}
	seen := make(map[string]bool, len(swaps))
	for _, s := range swaps {
		if err := validateKey(op, t, s.Key); err != nil {
			return err
		}
		if seen[s.Key] {
			return opError(op, t, s.Key, "key appears twice")
		}
		seen[s.Key] = true
		if len(s.Value) > maxValueLen {
			return opError(op, t, s.Key, "value longer than "+strconv.Itoa(maxValueLen)+" bytes")
		}
	}
	return nil
}

func validateKey(op string, t store.Tenant, key string) error {
	switch {
	case t < 0:
		return opError(op, t, key, "negative tenant")
	case key == "":
		return opError(op, t, key, "empty key")
	case len(key) > maxKeyLen:
		return opError(op, t, key, "key longer than "+strconv.Itoa(maxKeyLen)+" bytes")
	case strings.ContainsRune(key, 0):
		return opError(op, t, key, "key contains a NUL byte")
	}
	return nil
}
