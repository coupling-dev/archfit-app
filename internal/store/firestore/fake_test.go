package firestore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeToken = "fake-access-token"

// fakeFirestore serves the Firestore REST subset the driver uses, with the semantics the driver relies on:
// commits are atomic and check every precondition first; each write gets a new update time; a failed
// precondition answers FAILED_PRECONDITION, ALREADY_EXISTS or NOT_FOUND, like Firestore. It refuses every
// request shape it does not know, so the driver cannot drift from the subset unnoticed.
type fakeFirestore struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	docs    map[string]fakeDoc
	clock   time.Time
	commits int
	faults  []fault
	// beforeCommit, when set, runs once with the lock held when the next commit arrives, before any check:
	// another instance's write that lands between the driver's read and its commit.
	beforeCommit func(*fakeFirestore)
	// streamErr, when set, ends the next streamed answer with an error element after a 200 status,
	// as Firestore does when a stream fails after its first element.
	streamErr string
}

type fakeDoc struct {
	fields     map[string]fsValue
	updateTime string
}

// fault replaces the answer to the next commit. apply runs the commit before the fault answers, like a
// response lost after Firestore wrote.
type fault struct {
	httpStatus int
	status     string
	apply      bool
}

func newFake(t *testing.T) *fakeFirestore {
	t.Helper()
	f := &fakeFirestore{t: t, docs: map[string]fakeDoc{}, clock: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a driver for project p on the fake, as one App instance would hold it.
func (f *fakeFirestore) client(t *testing.T) *driver {
	t.Helper()
	s, err := New(Config{ProjectID: "archfit-test"}, staticToken(fakeToken), f.srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := s.(*driver)
	d.base = f.srv.URL + "/v1/"
	return d
}

func (f *fakeFirestore) inject(faults ...fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = append(f.faults, faults...)
}

func (f *fakeFirestore) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits
}

func (f *fakeFirestore) put(name string, fields map[string]fsValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[name] = fakeDoc{fields: fields, updateTime: f.tick()}
}

func (f *fakeFirestore) tick() string {
	f.clock = f.clock.Add(time.Microsecond)
	return f.clock.Format("2006-01-02T15:04:05.000000Z")
}

const fakeDocuments = "projects/archfit-test/databases/(default)/documents"

func (f *fakeFirestore) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		rpcError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing credentials")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && path == fakeDocuments+":batchGet":
		f.batchGet(w, r)
	case r.Method == http.MethodPost && path == fakeDocuments+":commit":
		f.commit(w, r)
	case r.Method == http.MethodPost && path == fakeDocuments+":runQuery":
		f.runQuery(w, r)
	default:
		// Firestore answers a request for a database or project that does not exist like this.
		rpcError(w, http.StatusNotFound, "NOT_FOUND", "the database "+path+" does not exist")
	}
}

func (f *fakeFirestore) batchGet(w http.ResponseWriter, r *http.Request) {
	var req batchGetRequest
	if err := decodeStrict(r, &req); err != nil || len(req.Documents) == 0 {
		rpcError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad batchGet")
		return
	}
	readTime := f.clock.Format(time.RFC3339Nano)
	out := []map[string]any{}
	// Firestore does not keep the request order, so the fake answers in reverse order.
	for _, name := range slices.Backward(req.Documents) {
		if doc, ok := f.docs[name]; ok {
			out = append(out, map[string]any{"found": document{Name: name, Fields: doc.fields, UpdateTime: doc.updateTime}, "readTime": readTime})
		} else {
			out = append(out, map[string]any{"missing": name, "readTime": readTime})
		}
	}
	f.writeStream(w, out)
}

// writeStream answers a streaming method, with the injected error element last when one is set.
func (f *fakeFirestore) writeStream(w http.ResponseWriter, out []map[string]any) {
	if f.streamErr != "" {
		out = append(out, map[string]any{"error": map[string]any{"code": 500, "status": f.streamErr, "message": "stream failed"}})
		f.streamErr = ""
	}
	writeJSON(w, out)
}

func (f *fakeFirestore) commit(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if err := decodeStrict(r, &req); err != nil || len(req.Writes) == 0 {
		rpcError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad commit")
		return
	}
	f.commits++
	// Firestore names the document in its messages; the driver must never pass a message on.
	named := " on " + req.Writes[0].Delete
	if req.Writes[0].Update != nil {
		named = " on " + req.Writes[0].Update.Name
	}
	if hook := f.beforeCommit; hook != nil {
		f.beforeCommit = nil
		hook(f)
	}
	if len(f.faults) > 0 {
		ft := f.faults[0]
		f.faults = f.faults[1:]
		if ft.apply {
			if status, code := f.apply(req.Writes); status != "" {
				rpcError(w, code, status, "precondition failed"+named)
				return
			}
		}
		rpcError(w, ft.httpStatus, ft.status, "injected"+named)
		return
	}
	if status, code := f.apply(req.Writes); status != "" {
		rpcError(w, code, status, "precondition failed"+named)
		return
	}
	results := make([]map[string]string, len(req.Writes))
	for i := range results {
		results[i] = map[string]string{"updateTime": f.clock.Format("2006-01-02T15:04:05.000000Z")}
	}
	writeJSON(w, map[string]any{"writeResults": results, "commitTime": f.clock.Format(time.RFC3339Nano)})
}

// apply checks every precondition, then applies every write. It returns the RPC status of the first failure.
func (f *fakeFirestore) apply(writes []write) (string, int) {
	for _, wr := range writes {
		name := wr.Delete
		if wr.Update != nil {
			name = wr.Update.Name
		}
		if (wr.Update == nil) == (wr.Delete == "") || !strings.HasPrefix(name, fakeDocuments+"/"+collection+"/") {
			return "INVALID_ARGUMENT", http.StatusBadRequest
		}
		cur, exists := f.docs[name]
		pre := wr.CurrentDocument
		switch {
		case pre == nil:
		case pre.Exists != nil && pre.UpdateTime != "":
			return "INVALID_ARGUMENT", http.StatusBadRequest
		case pre.Exists != nil && !*pre.Exists && exists:
			return "ALREADY_EXISTS", http.StatusConflict
		case pre.Exists != nil && *pre.Exists && !exists:
			return "NOT_FOUND", http.StatusNotFound
		case pre.UpdateTime != "" && (!exists || cur.updateTime != pre.UpdateTime):
			return "FAILED_PRECONDITION", http.StatusBadRequest
		}
	}
	now := f.tick()
	for _, wr := range writes {
		if wr.Delete != "" {
			delete(f.docs, wr.Delete)
			continue
		}
		f.docs[wr.Update.Name] = fakeDoc{fields: wr.Update.Fields, updateTime: now}
	}
	return "", 0
}

// runQuery supports one filter on the integer field tenant (EQUAL or GREATER_THAN), an optional ascending
// order on it, a limit and a projection, over the collection kv.
func (f *fakeFirestore) runQuery(w http.ResponseWriter, r *http.Request) {
	var req runQueryRequest
	q := &req.StructuredQuery
	if err := decodeStrict(r, &req); err != nil || len(q.From) != 1 || q.From[0].CollectionID != collection ||
		q.Where == nil || q.Where.FieldFilter == nil || q.Where.FieldFilter.Field.FieldPath != fieldTenant ||
		(len(q.OrderBy) > 1) || (len(q.OrderBy) == 1 && (q.OrderBy[0].Field.FieldPath != fieldTenant || q.OrderBy[0].Direction != "ASCENDING")) {
		rpcError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported query")
		return
	}
	want, err := q.Where.FieldFilter.Value.integer()
	if err != nil {
		rpcError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported filter value")
		return
	}
	type hit struct {
		name   string
		tenant int64
	}
	var hits []hit
	for name, doc := range f.docs {
		tn, err := doc.fields[fieldTenant].integer()
		if err != nil {
			continue // Firestore leaves a document without the field out of the index
		}
		switch q.Where.FieldFilter.Op {
		case "EQUAL":
			if tn != want {
				continue
			}
		case "GREATER_THAN":
			if tn <= want {
				continue
			}
		default:
			rpcError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported operator")
			return
		}
		hits = append(hits, hit{name, tn})
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if len(q.OrderBy) == 1 && a.tenant != b.tenant {
			return int(a.tenant - b.tenant)
		}
		return strings.Compare(a.name, b.name)
	})
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	readTime := f.clock.Format(time.RFC3339Nano)
	out := []map[string]any{}
	for _, h := range hits {
		fields := map[string]fsValue{}
		if q.Select != nil {
			for _, ref := range q.Select.Fields {
				if v, ok := f.docs[h.name].fields[ref.FieldPath]; ok {
					fields[ref.FieldPath] = v
				}
			}
		} else {
			fields = f.docs[h.name].fields
		}
		out = append(out, map[string]any{
			"document": document{Name: h.name, Fields: fields, UpdateTime: f.docs[h.name].updateTime},
			"readTime": readTime,
		})
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"readTime": readTime})
	}
	f.writeStream(w, out)
}

func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func rpcError(w http.ResponseWriter, httpStatus int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": httpStatus, "message": fmt.Sprint(message), "status": status,
	}})
}
