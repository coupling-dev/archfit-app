package feedback

import (
	"encoding/json"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestPageUsesTheCommentModel(t *testing.T) {
	r := engineResult(t, "waiting")
	r.Errors = append(r.Errors, r.Errors[0], r.Errors[0])
	r.Errors[1].Path = "second.go"
	r.Errors[1].Next.Text = "wait for an owner: #432"
	page := BuildPage(r)
	if page.Status != result.StatusWaiting || page.StatusLine != "Archfit: waiting, request #431" {
		t.Errorf("status = %q %q", page.Status, page.StatusLine)
	}
	if page.Author != "nothing to do. This check runs again when #431 and #432 are answered." || page.Owner != "answer #431 and #432." {
		t.Errorf("author = %q; owner = %q", page.Author, page.Owner)
	}
	for i, want := range []int{431, 432, 431} {
		if page.Records[i].Request != want || page.Records[i].ShowNext {
			t.Errorf("record %d = %+v", i, page.Records[i])
		}
	}
	if page.Records[1].Path != "second.go" {
		t.Error("records changed order")
	}
}

func TestPageKeepsNextStepsTheAuthorLineDoesNotName(t *testing.T) {
	r := engineResult(t, "graph")
	r.Errors = append(r.Errors, engineResult(t, "deny").Errors[0])
	page := BuildPage(r)
	if page.Records[0].ShowNext || !page.Records[1].ShowNext {
		t.Errorf("next steps = %+v", page.Records)
	}
	if page.Records[0].Next.Text != r.Errors[0].Next.Text {
		t.Error("the hidden step was lost")
	}
}

func TestBuildingAPageDoesNotChangeTheResult(t *testing.T) {
	r := engineResult(t, "graph")
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	page := BuildPage(r)
	page.Records[0].What = "changed"
	page.Records[0].Balance.Value = 0
	if r.Errors[0].What != engineResult(t, "graph").Errors[0].What || r.Errors[0].Balance.Value != 9 {
		t.Fatal("page shares mutable fields with the source")
	}
	after, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("building a page changed the result")
	}
}
