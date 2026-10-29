package internal_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

type screenLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// linkSentence follows the identity line of a comment that links to the page.
const linkSentence = " The App link shows the current PR result/recovery diagnostics, not a historical snapshot."

var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// commentScreen is the comment before its first fold as lines of text. What only a comment carries (its markers and its
// link to the page) is not part of the first screen.
func commentScreen(comment string) []screenLine {
	head, _, _ := strings.Cut(comment, "\n\n<details>")
	var out []screenLine
	for _, raw := range strings.Split(head, "\n") {
		raw = strings.TrimSpace(raw)
		kind := "text"
		switch {
		case raw == "" || strings.HasPrefix(raw, "<!--") || strings.HasPrefix(raw, "[Details in Archfit]"):
			continue
		case strings.HasPrefix(raw, "## Archfit: "):
			kind, raw = "headline", strings.TrimPrefix(raw, "## Archfit: ")
		case strings.HasPrefix(raw, "### "):
			kind, raw = "heading", strings.TrimPrefix(raw, "### ")
		case strings.HasPrefix(raw, "- "):
			kind, raw = "item", strings.TrimPrefix(raw, "- ")
		}
		raw = strings.NewReplacer("**", "", "`", "", "&lt;", "<", "&gt;", ">").Replace(markdownLink.ReplaceAllString(raw, "$1"))
		out = append(out, screenLine{kind, strings.TrimSuffix(raw, linkSentence)})
	}
	return out
}

// The pull request page tells the story of the comment: for the same published result its first screen is the comment's
// first screen, line for line, whether the pull request passes or fails.
func TestPullPageFirstScreenEqualsTheCommentsFirstScreen(t *testing.T) {
	for _, tt := range []struct {
		fixture, conclusion string
	}{
		{"real-healthy", "success"},
		{"real-blocked-no-baseline", "failure"},
		{"real-blocked-forbidden-and-allowed", "failure"},
		{"real-waiver-active", "success"},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			f := loadFixture(t, tt.fixture)
			w := newWorld(t)
			w.seedDemo(f, "required", true)
			w.allow(owner, demoRepo, "read")
			w.keepReport(demoRepo, demoRun, f.report)
			s := newSystem(t, w, options{})
			if got := s.upload(demoUpload(f)); got.Status != http.StatusOK || got.Conclusion != tt.conclusion {
				t.Fatalf("answer = %+v; want %s", got, tt.conclusion)
			}
			comments := w.commentsOf(demoRepo, demoPull)
			if len(comments) != 1 {
				t.Fatalf("comments = %d; want 1", len(comments))
			}

			b := newBrowser(t, s.do)
			b.signIn(owner)
			resp, raw := b.get("/api/repos/" + demoRepo + "/pulls/42")
			var page struct {
				FirstScreen []screenLine `json:"first_screen"`
			}
			if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil {
				t.Fatalf("pull request page = %d %.300s", resp.StatusCode, raw)
			}
			want := commentScreen(comments[0].Body)
			if len(want) < 4 || want[0].Kind != "headline" {
				t.Fatalf("comment first screen = %+v", want)
			}
			if len(page.FirstScreen) != len(want) {
				t.Fatalf("page first screen:\n%+v\ncomment first screen:\n%+v", page.FirstScreen, want)
			}
			for i := range want {
				if page.FirstScreen[i] != want[i] {
					t.Errorf("line %d: page %+v, comment %+v", i, page.FirstScreen[i], want[i])
				}
			}
		})
	}
}

// The decisions page reads through the seam control hands ui: a repository with no request answers empty lists.
func TestDecisionsPageIsServedThroughTheWiredQueue(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	w.allow(owner, demoRepo, "read")
	s := newSystem(t, w, options{})
	b := newBrowser(t, s.do)
	b.signIn(owner)
	resp, raw := b.get("/api/repos/" + demoRepo + "/decisions")
	var page struct {
		Requests []any `json:"requests"`
		Error    bool  `json:"requests_error"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil || page.Requests == nil || page.Error {
		t.Fatalf("decisions page = %d %.300s; want empty lists and no error", resp.StatusCode, raw)
	}
}
