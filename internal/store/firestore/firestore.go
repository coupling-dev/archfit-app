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

	maxResponseBytes = 8 << 20
	// deleteBatch is the number of documents DeleteTenant reads and deletes in one commit. The deleted documents
	// count toward the 10 MiB limit of one commit: 100 values of 64 KiB, with their index entries, stay under it.
	deleteBatch = 100

	// maxAttempts bounds the read-compare-commit loop of CompareAndSwap and Add on a conflict. A local lock
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

// lock serializes CompareAndSwap and Add on one document inside this process. It only lowers contention:
// atomicity across instances comes from the commit preconditions. A caller whose context ends while it
// waits leaves with the context error.
func (d *driver) lock(ctx context.Context, name string) (func(), error) {
	slot := d.locks[maphash.String(d.seed, name)%lockStripes]
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *driver) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateKey("get", t, key); err != nil {
		return nil, err
	}
	doc, found, err := d.read(ctx, d.docName(t, key))
	switch {
	case err != nil:
		return nil, err
	case !found:
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
	err := d.readModifyWrite(ctx, name, func(cur stored, found bool) (*write, error) {
		if found != (old != nil) || !bytes.Equal(cur.value, old) {
			swapped = false
			return nil, nil
		}
		swapped = true
		if value == nil {
			if !found {
				return nil, nil // nothing to delete; the comparison held at the read
			}
			return &write{Delete: name, CurrentDocument: precondition(cur, found)}, nil
		}
		w := d.update(t, key, value, precondition(cur, found))
		return &w, nil
	})
	if err != nil {
		return false, err
	}
	return swapped, nil
}

func (d *driver) Add(ctx context.Context, t store.Tenant, key string, delta int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateKey("add", t, key); err != nil {
		return 0, err
	}
	var next int64
	err := d.readModifyWrite(ctx, d.docName(t, key), func(cur stored, found bool) (*write, error) {
		var n int64
		if found {
			var err error
			// The strconv error quotes its input, so it is dropped: errors never carry a value.
			if n, err = strconv.ParseInt(string(cur.value), 10, 64); err != nil || strconv.FormatInt(n, 10) != string(cur.value) {
				return nil, opError("add", t, key, "value is not a counter")
			}
		}
		if (delta > 0 && n > math.MaxInt64-delta) || (delta < 0 && n < math.MinInt64-delta) {
			return nil, opError("add", t, key, "counter overflow")
		}
		next = n + delta
		w := d.update(t, key, strconv.AppendInt(nil, next, 10), precondition(cur, found))
		return &w, nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// readModifyWrite reads the document, lets decide compute the write, and commits it under a precondition on
// what was read. On a conflict, which is a definite rejection, it reads again. Any other commit failure is
// returned as is: the write may have landed, so a retry could report a swap it made as a failed comparison.
func (d *driver) readModifyWrite(ctx context.Context, name string, decide func(stored, bool) (*write, error)) error {
	unlock, err := d.lock(ctx, name)
	if err != nil {
		return err
	}
	defer unlock()
	for attempt := 1; ; attempt++ {
		cur, found, err := d.read(ctx, name)
		if err != nil {
			return err
		}
		w, err := decide(cur, found)
		if err != nil || w == nil {
			return err
		}
		err = d.commit(ctx, *w)
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
func precondition(cur stored, found bool) *precond {
	if !found {
		f := false
		return &precond{Exists: &f}
	}
	return &precond{UpdateTime: cur.updateTime}
}

// stored is a decoded document.
type stored struct {
	value      []byte // non-nil, also when empty
	updateTime string // opaque; only passed back in a precondition
}

// read uses batchGet rather than a plain GET: batchGet reports a missing document as "missing", so a
// NOT_FOUND status always means a missing database or project and stays an error, never a missing key.
func (d *driver) read(ctx context.Context, name string) (stored, bool, error) {
	items, err := d.stream(ctx, d.documents+":batchGet", batchGetRequest{Documents: []string{name}})
	if err != nil {
		return stored{}, false, err
	}
	var doc *document
	missing := false
	for _, it := range items {
		switch {
		case it.Found != nil && it.Found.Name == name:
			doc = it.Found
		case it.Missing == name:
			missing = true
		}
	}
	switch {
	case missing && doc == nil:
		return stored{}, false, nil
	case doc == nil || missing:
		return stored{}, false, errors.New("store: firestore: batchGet did not answer for the document")
	}
	v, ok := doc.Fields[fieldValue]
	if !ok || v.BytesValue == nil || doc.UpdateTime == "" {
		return stored{}, false, errCorrupt
	}
	b, err := base64.StdEncoding.DecodeString(*v.BytesValue)
	if err != nil {
		return stored{}, false, errCorrupt
	}
	if b == nil {
		b = []byte{}
	}
	return stored{value: b, updateTime: doc.UpdateTime}, true, nil
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
