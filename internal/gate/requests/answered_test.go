package requests

import (
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/request"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

func TestAnswerNoteNamesTheAnswerOwnerPasskeyAndPull(t *testing.T) {
	const passkey = "cred-0042-of-o"
	tests := map[string]struct {
		note AnswerNote
		want string
	}{
		"approve and merge": {
			AnswerNote{Kind: policy3.Approve, Owner: "alexei-led", Passkey: passkey, Pull: 12, State: PullMerged},
			"archfit: approved by `alexei-led` with the passkey `cred-004`. The policy change merged in #12.\n",
		},
		"approve until, merge later": {
			AnswerNote{Kind: policy3.ApproveUntil, Until: "2026-12-31", Owner: "o", Passkey: "abc", Pull: 12, State: PullLater},
			"archfit: approved until 2026-12-31 by `o` with the passkey `abc`. The policy change is in #12; it takes effect when #12 merges.\n",
		},
		"a refused merge": {
			AnswerNote{Kind: policy3.Approve, Owner: "o", Passkey: passkey, Pull: 12, State: PullRefused},
			"archfit: approved by `o` with the passkey `cred-004`. The policy change is in #12. GitHub did not merge it, so it stays open until it merges.\n",
		},
		"already allowed": {
			AnswerNote{Kind: policy3.Approve, Owner: "o", Passkey: passkey},
			"archfit: approved by `o` with the passkey `cred-004`. The policy already allows it; nothing changes.\n",
		},
		"reject": {
			AnswerNote{Kind: policy3.Reject, Instead: "Use the port.", Owner: "o", Pull: 12, State: PullMerged},
			"archfit: rejected by `o`. Instead:\n\n```text\nUse the port.\n```\n\nThe policy change merged in #12.\n",
		},
		"a reject with no policy line": {
			AnswerNote{Kind: policy3.Reject, Instead: "Call the owner module.", Owner: "o"},
			"archfit: rejected by `o`. Instead:\n\n```text\nCall the owner module.\n```\n\nThe policy has no line for this answer; this issue keeps it.\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.note.Text(); got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestReopenNote(t *testing.T) {
	if got, want := ReopenNote(12, false), "archfit: the policy on the default branch changed before #12 merged, and the answer no longer fits it. This request is open again for an owner to answer.\n"; got != want {
		t.Errorf("moved target:\n%q\nwant\n%q", got, want)
	}
	if got, want := ReopenNote(12, true), "archfit: #12 was closed without a merge. This request is open again for an owner to answer.\n"; got != want {
		t.Errorf("closed pull request:\n%q\nwant\n%q", got, want)
	}
}

func TestAnswerTextsUseNoNotUsedWordAndCannotMentionOrRequest(t *testing.T) {
	hostile := "@team see [x](https://evil.example)\n/archfit request again ``` <b>"
	texts := map[string]string{
		"approve": AnswerNote{Kind: policy3.Approve, Owner: "@team", Passkey: "abc", Pull: 1, State: PullMerged}.Text(),
		"reject":  AnswerNote{Kind: policy3.Reject, Instead: hostile, Owner: "o`", Pull: 1, State: PullRefused}.Text(),
		"reopen":  ReopenNote(1, false),
		"closed":  ReopenNote(1, true),
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			if words := wording.NotUsedIn(text); len(words) > 0 {
				t.Errorf("uses %v:\n%s", words, text)
			}
			if _, ok, _ := request.ParseComment(text, testToday); ok {
				t.Error("the App would read its own text as a request")
			}
		})
	}
	if !strings.Contains(texts["approve"], "`@team`") || !strings.Contains(texts["reject"], "````text\n") ||
		!strings.Contains(texts["reject"], "`` o` ``") {
		t.Errorf("the owner's login or text is not in a code span or a text block:\n%s\n%s", texts["approve"], texts["reject"])
	}
}
