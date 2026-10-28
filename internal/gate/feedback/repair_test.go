package feedback

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func repairPublication(o verdict.Outcome) Publication {
	return Publication{
		Target:   pushTarget(headB),
		Identity: fullIdentity(),
		Outcome:  o,
		Receipt:  receiptFor(o, 7, 1),
		Report:   stressReport(),
		Trusted:  trusted(settings.GateRequired),
	}
}

// The issue body is a GitHub body like the comment: under the limit, with whole tasks and the markers intact.
func TestRepairBodyStaysWithinGitHubsLimit(t *testing.T) {
	p := repairPublication(outcome(verdict.Failure, reasonBlocking, reasonCoverage))
	body := renderRepair(p)

	if len(body) > maxBody || !utf8.ValidString(body) {
		t.Fatalf("body is %d bytes, valid UTF-8 %v; want at most %d", len(body), utf8.ValidString(body), maxBody)
	}
	if want := repairMarker("main") + "\n" + headMarker(headB, p.Receipt) + "\n"; !strings.HasPrefix(body, want) {
		t.Errorf("body does not start with the repair and head markers:\n%.200s", body)
	}
	if got, ok := parseReceipt(body); !ok || got != p.Receipt {
		t.Errorf("receipt marker damaged: %+v ok=%v", got, ok)
	}
	if kept := len(decodeTasks(t, body)); kept == 0 || kept >= len(p.Report.AgentTasks) {
		t.Errorf("task block holds %d tasks, want some but not all", kept)
	}
	mustContain(t, body, "Cause: Blocking findings.", "**Next:** "+reasonBlocking.Next, "more tasks did not fit", fixedTrustSentence)
}

// Engine and repository strings cannot forge a marker, close a fence or add markup.
func TestRepairBodyIsSanitized(t *testing.T) {
	o := outcome(verdict.Failure, verdict.Reason{Code: verdict.ReasonBlockingFindings, Category: verdict.CategoryArchitecture, Detail: evil, Next: evil})
	p := repairPublication(o)
	p.Report = evilReport()
	p.Target.Repo.DefaultBranch = "main--><b>x</b>`tick`"
	body := renderRepair(p)

	// repair, head, receipt, and the task delimiters
	if got := strings.Count(body, "<!--"); got != 5 {
		t.Errorf("%d marker openings, want 5", got)
	}
	if got := strings.Count(body, "-->"); got != 5 {
		t.Errorf("%d marker closings, want 5", got)
	}
	mustNotContain(t, body, "<b>", "`tick`")
	if !strings.HasPrefix(body, repairMarker(p.Target.Repo.DefaultBranch)+"\n") {
		t.Errorf("the branch broke the repair marker:\n%.200s", body)
	}
	if tasks := decodeTasks(t, body); len(tasks) != 1 || tasks[0]["goal"] != evil {
		t.Errorf("agent task goal was not preserved as decoded: %v", tasks)
	}
}

// A suspended subscription withholds paid output in the issue as everywhere else.
func TestRepairBodyWithholdsPaidOutputWhenSuspended(t *testing.T) {
	body := renderRepair(repairPublication(outcome(verdict.ActionRequired, reasonSuspend)))
	mustContain(t, body, "Cause: Subscription suspended.", reasonSuspend.Next)
	mustNotContain(t, body, tasksOpen, headBlocking, headCommands)
}
