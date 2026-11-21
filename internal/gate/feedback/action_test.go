package feedback

import (
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/wire/result"
	"github.com/coupling-dev/archfit-app/internal/wire/wording"
)

func workflowUnknown() result.Result {
	return result.Result{Status: result.StatusUnknown, Summary: "archfit: unknown, no verdict", Unknown: []result.Unknown{{
		Path: ".github/workflows/archfit.yaml", Cause: "tool", What: "differs from main",
		Why:  "a pull request runs its own workflow file",
		Next: result.Next{Kind: nextFix, Text: "update the branch; if the pull request changes the workflow, an owner approves it in the App"},
	}}}
}

func TestAPendingOwnerStepShowsOneLinkOnTheCheckAndTheComment(t *testing.T) {
	const link = appURL + "/r/acme/shop/owner/workflow?pull=7"
	h := newHarness(headB)
	p := pub4(prTarget(headB), 7, 1, workflowUnknown())
	p.OwnerPage = OwnerPage{Label: "Review the workflow change", Path: "/owner/workflow?pull=7", Role: "owner"}
	h.publish4(t, p)

	run := h.checks.latest(t, headB, gateCheck)
	if run.DetailsURL != link {
		t.Errorf("details %q, want the action page %q", run.DetailsURL, link)
	}
	if run.Conclusion != conclusionActionRequired || run.Title != "⏳ Waiting for an owner: Review the workflow change" {
		t.Errorf("check = %q %q", run.Conclusion, run.Title)
	}
	next := "**Next:** [Review the workflow change](" + link + ") (owner, passkey)\n"
	if !strings.HasPrefix(run.Summary, next) {
		t.Errorf("summary does not start with the link:\n%s", run.Summary)
	}
	mustContain(t, run.Summary, "| Who can act | an owner named in the approved policy |", "| Head | `"+shortHead(headB)+"` |", "differs from main")
	body := h.comments.all()[0].Body
	mustContain(t, body, "## ⏳ Waiting for an owner: Review the workflow change\n\n"+next+"\n| | |\n| --- | --- |\n| Who can act | an owner named in the approved policy |\n",
		"<details><summary>Details</summary>\n\n**Archfit: unknown, no verdict**\n", "differs from main", "\n</details>\n")
	if strings.Contains(strings.SplitN(body, "<details>", 2)[0], "differs from main") {
		t.Errorf("the record is not folded:\n%s", body)
	}
	for name, text := range map[string]string{"title": run.Title, "summary": run.Summary, "comment": body} {
		if found := wording.NotUsedIn(text); len(found) > 0 {
			t.Errorf("%s uses %q", name, found)
		}
	}
}

func TestWithoutAnOwnerStepTheDetailsLinkIsThePullPage(t *testing.T) {
	h := newHarness(headB)
	h.publish4(t, pub4(prTarget(headB), 7, 1, workflowUnknown()))
	run := h.checks.latest(t, headB, gateCheck)
	if run.DetailsURL != appURL+"/r/acme/shop/pull/7" || strings.Contains(run.Summary, "**Next:**") {
		t.Errorf("details %q, summary:\n%s", run.DetailsURL, run.Summary)
	}
}
