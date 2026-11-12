package request_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/request"
)

// today is the App's date of every golden: the date of the engine's result goldens.
var today = time.Date(2026, 11, 10, 15, 4, 5, 0, time.UTC)

// TestLineGoldens: each canonical line the CLI posts parses to its request, and is the canonical line of that request.
func TestLineGoldens(t *testing.T) {
	for _, in := range glob(t, "testdata/line/*.txt") {
		t.Run(name(in), func(t *testing.T) {
			var want request.Line
			decodeStrict(t, sibling(in, ".json"), &want)
			body := string(read(t, in))
			got, ok, err := request.ParseComment(body, today)
			if err != nil || !ok {
				t.Fatalf("ParseComment: ok %v, err %v", ok, err)
			}
			if got != want {
				t.Errorf("parsed %+v, want %+v", got, want)
			}
			if canon := canonicalLine(want) + "\n"; body != canon {
				t.Errorf("not the canonical line:\n got %q\nwant %q", body, canon)
			}
		})
	}
}

// TestFormGoldens: each issue body the CLI writes parses to its request, record and answer, and is the canonical
// body of them.
func TestFormGoldens(t *testing.T) {
	for _, in := range glob(t, "testdata/form/*.md") {
		t.Run(name(in), func(t *testing.T) {
			var want request.Form
			decodeStrict(t, sibling(in, ".json"), &want)
			body := string(read(t, in))
			got, ok, err := request.ParseIssue(body, today)
			if err != nil || !ok {
				t.Fatalf("ParseIssue: ok %v, err %v", ok, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parsed %+v, want %+v", got, want)
			}
			if canon := canonicalLine(want.Line) + "\n\n```text\n" + want.Record + "```\n"; body != canon {
				t.Errorf("not the canonical body:\n got %q\nwant %q", body, canon)
			}
		})
	}
}

// TestCommentGoldens: comments as the App receives them.
func TestCommentGoldens(t *testing.T) {
	for _, in := range glob(t, "testdata/comment/*.txt") {
		t.Run(name(in), func(t *testing.T) {
			var want request.Line
			decodeStrict(t, sibling(in, ".json"), &want)
			got, ok, err := request.ParseComment(string(read(t, in)), today)
			if err != nil || !ok {
				t.Fatalf("ParseComment: ok %v, err %v", ok, err)
			}
			if got != want {
				t.Errorf("parsed %+v, want %+v", got, want)
			}
		})
	}
}

// TestRejectGoldens: each request line that cannot be used gets its reply, as a comment and as an issue.
func TestRejectGoldens(t *testing.T) {
	for _, in := range glob(t, "testdata/reject/*.txt") {
		t.Run(name(in), func(t *testing.T) {
			want := string(read(t, sibling(in, ".md")))
			body := string(read(t, in))
			_, ok, err := request.ParseComment(body, today)
			assertReply(t, "ParseComment", ok, err, want)
			_, ok, err = request.ParseIssue(body, today)
			assertReply(t, "ParseIssue", ok, err, want)
		})
	}
	for golden, r := range map[string]*request.Rejection{
		"testdata/reject/nothing-to-ask.md": request.NothingToAsk(string(read(t, "testdata/reject/nothing-to-ask.record"))),
		"testdata/reject/no-error.md":       request.NothingToAsk(""),
		"testdata/reject/no-permission.md":  request.NoPermission(),
	} {
		if got, want := r.Reply(), string(read(t, golden)); got != want {
			t.Errorf("%s:\n--- got\n%s--- want\n%s", golden, got, want)
		}
	}
}

func assertReply(t *testing.T, parser string, ok bool, err error, want string) {
	t.Helper()
	var r *request.Rejection
	if !ok || !errors.As(err, &r) {
		t.Fatalf("%s: ok %v, err %v; want a rejection", parser, ok, err)
	}
	if got := r.Reply(); got != want {
		t.Errorf("%s reply:\n--- got\n%s--- want\n%s", parser, got, want)
	}
}

// TestIssueGoldens: each request renders to its title and body, and the body is never read as a request.
func TestIssueGoldens(t *testing.T) {
	for _, in := range glob(t, "testdata/issue/*.json") {
		t.Run(name(in), func(t *testing.T) {
			var issue request.Issue
			decodeStrict(t, in, &issue)
			got := issue.Title() + "\n\n" + issue.Body()
			if want := string(read(t, sibling(in, ".md"))); got != want {
				t.Errorf("rendered:\n--- got\n%s--- want\n%s", got, want)
			}
			if _, ok, _ := request.ParseIssue(issue.Body(), today); ok {
				t.Error("the rendered body reads as a request")
			}
		})
	}
}

// TestEveryGoldenHasItsCase: every file under testdata/ is read by a test above.
func TestEveryGoldenHasItsCase(t *testing.T) {
	read := map[string]bool{"testdata/wording.go.txt": true, "testdata/reject/nothing-to-ask.record": true}
	for _, pair := range []struct{ glob, other string }{
		{"testdata/line/*.txt", ".json"}, {"testdata/form/*.md", ".json"}, {"testdata/comment/*.txt", ".json"},
		{"testdata/reject/*.txt", ".md"}, {"testdata/issue/*.json", ".md"},
	} {
		for _, in := range glob(t, pair.glob) {
			read[in], read[sibling(in, pair.other)] = true, true
		}
	}
	for _, golden := range []string{"no-error", "no-permission", "nothing-to-ask"} {
		read["testdata/reject/"+golden+".md"] = true
	}
	for _, path := range allGoldens(t) {
		if !read[path] {
			t.Errorf("%s is read by no test", path)
		}
	}
}

// TestRepliesAreNotRequests: a reply the App posts never reads as a request, so the App cannot answer itself.
func TestRepliesAreNotRequests(t *testing.T) {
	for _, path := range glob(t, "testdata/reject/*.md") {
		if _, ok, _ := request.ParseComment(string(read(t, path)), today); ok {
			t.Errorf("%s reads as a request", path)
		}
	}
}

// canonicalLine is the line the CLI posts for l (package doc).
func canonicalLine(l request.Line) string {
	line := request.Command + " " + l.Reason
	if l.Expires != "" {
		line += " --expires " + l.Expires
	}
	return line
}

func allGoldens(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir("testdata", func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, filepath.ToSlash(path))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func glob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no golden matches %s", pattern)
	}
	return paths
}

func name(path string) string { return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) }

func sibling(path, ext string) string { return strings.TrimSuffix(path, filepath.Ext(path)) + ext }

func decodeStrict(t *testing.T, path string, into any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(read(t, path)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed test data paths
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
