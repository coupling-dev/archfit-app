package gate

import (
	"testing"
	"time"
)

func TestOwnerActionsOrderWorkflowChangesByWaitThenNumber(t *testing.T) {
	old, recent := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	view := HomeState{
		OutsideFlow: true, PolicyAvailable: true,
		WorkflowPulls:   []int{3, 5, 7, 9},
		WorkflowUpdated: map[int]time.Time{3: recent, 5: old, 9: old},
	}

	var got []string
	for _, a := range view.OwnerActions() {
		got = append(got, a.Kind+":"+a.Path)
	}

	want := []string{"confirm:/owner/confirm", "revert:/owner/revert",
		"workflow:/owner/workflow?pull=5", "workflow:/owner/workflow?pull=9", "workflow:/owner/workflow?pull=3", "workflow:/owner/workflow?pull=7"}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("actions = %v, want %v", got, want)
		}
	}
}

func TestOwnerActionsOfAPolicyWithoutAFileOnlyOffersRevert(t *testing.T) {
	got := HomeState{OutsideFlow: true}.OwnerActions()
	if len(got) != 1 || got[0].Kind != OwnerRevert {
		t.Fatalf("actions = %+v, want only a revert", got)
	}
}
