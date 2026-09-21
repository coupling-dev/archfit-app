package account

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

const none = -1 // a zero time in the plan table

type grantStep struct{ until, at int }

type planCase struct {
	name        string
	private     bool // Observe(true) at day 0
	grants      []grantStep
	now         int
	wantPlan    Plan
	wantSuspend int
	wantWarn    int
	wantGrant   int
}

var planCases = []planCase{
	{"no record", false, nil, 0, PlanPublic, none, none, none},
	{"day 0", true, nil, 0, PlanTrial, 37, 23, none},
	{"day 29", true, nil, 29, PlanTrial, 37, 23, none},
	{"day 30", true, nil, 30, PlanGrace, 37, 23, none},
	{"day 36", true, nil, 36, PlanGrace, 37, 23, none},
	{"day 37", true, nil, 37, PlanSuspended, 37, 23, none},
	{"grant to day 60 at day 10", true, []grantStep{{60, 5}}, 10, PlanGranted, 60, 53, 60},
	{"grant to day 60 at day 59", true, []grantStep{{60, 5}}, 59, PlanGranted, 60, 53, 60},
	{"grant to day 60 at day 60", true, []grantStep{{60, 5}}, 60, PlanSuspended, 60, 53, 60},
	{"grant to day 20 at day 25", true, []grantStep{{20, 5}}, 25, PlanTrial, 37, 23, 20},
	{"grant to day 20 at day 37", true, []grantStep{{20, 5}}, 37, PlanSuspended, 37, 23, 20},
	{"grant with no private repository", false, []grantStep{{60, 5}}, 10, PlanPublic, none, none, 60},
	{"grant cleared at day 40", true, []grantStep{{60, 5}, {none, 40}}, 40, PlanSuspended, 37, 23, none},
}

func dayOrZero(n int) time.Time {
	if n == none {
		return time.Time{}
	}
	return day(n)
}

func (pc planCase) apply(t *testing.T, a Account) {
	t.Helper()
	ctx := context.Background()
	if pc.private {
		mustNil(t, a.Observe(ctx, instA, true, day(0)))
	}
	for _, g := range pc.grants {
		mustNil(t, a.Grant(ctx, instA, dayOrZero(g.until), day(g.at)))
	}
}

func TestPlanStateMachine(t *testing.T) {
	for _, pc := range planCases {
		t.Run(pc.name, func(t *testing.T) {
			a, _, _, _ := newTest(store.NewMemory())
			pc.apply(t, a)
			st, err := a.Status(context.Background(), instA, day(pc.now))
			mustNil(t, err)
			if st.Plan != pc.wantPlan {
				t.Errorf("Plan = %q, want %q", st.Plan, pc.wantPlan)
			}
			wantTrialEnds := time.Time{}
			if pc.private {
				wantTrialEnds = day(30)
			}
			wantTime(t, "TrialEnds", st.TrialEnds, wantTrialEnds)
			wantTime(t, "SuspendAfter", st.SuspendAfter, dayOrZero(pc.wantSuspend))
			wantTime(t, "GrantUntil", st.GrantUntil, dayOrZero(pc.wantGrant))
		})
	}
}

func TestEntitlementFields(t *testing.T) {
	for _, pc := range planCases {
		t.Run(pc.name, func(t *testing.T) {
			s := store.NewMemory()
			a, _, _, _ := newTest(s)
			pc.apply(t, a)
			mustNil(t, a.Refresh(context.Background(), instA, day(pc.now)))
			e := readEntitlement(t, s, instA)
			wantTime(t, "SuspendAfter", e.SuspendAfter, dayOrZero(pc.wantSuspend))
			wantTime(t, "WarnFrom", e.WarnFrom, dayOrZero(pc.wantWarn))
			if pc.wantPlan == PlanPublic {
				if e.Notice != "" || e.Reason != "" {
					t.Errorf("public record has texts: %+v", e)
				}
				return
			}
			if e.Notice == "" {
				t.Error("Notice is empty for a non-public plan")
			}
			if !strings.Contains(e.Reason, "remove the Archfit requirement in the ruleset") {
				t.Errorf("Reason = %q, want the recovery action", e.Reason)
			}
		})
	}
}

func TestNoticeTexts(t *testing.T) {
	hour := time.Hour
	tests := []struct {
		name   string
		grants []grantStep
		now    time.Time
		want   string
	}{
		{"trial T-8d", nil, day(22), "Trial ends 2026-10-01"},
		{"trial T-7d", nil, day(23), "Trial ends 2026-10-01"},
		{"trial T-24h", nil, day(29), "Trial ends 2026-10-01 — 1 day left"},
		{"trial T-23h", nil, day(30).Add(-23 * hour), "Trial ends 2026-10-01 — 1 day left"},
		{"grace", nil, day(31), "Trial ended 2026-10-01. Archfit checks suspend on 2026-10-08"},
		{"granted", []grantStep{{60, 5}}, day(40), "Archfit beta access ends 2026-10-31"},
		{"granted G-23h", []grantStep{{60, 5}}, day(60).Add(-23 * hour), "Archfit beta access ends 2026-10-31 — 1 day left"},
		{"granted, grant ends inside trial", []grantStep{{20, 5}}, day(10), "Trial ends 2026-10-01"},
		{"granted, grant ends inside grace", []grantStep{{33, 5}}, day(31), "Trial ended 2026-10-01. Archfit checks suspend on 2026-10-08"},
		{"suspended", nil, day(37), "Archfit subscription inactive since 2026-10-08"},
		{"suspended after grant", []grantStep{{60, 5}}, day(61), "Archfit subscription inactive since 2026-10-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewMemory()
			a, _, _, _ := newTest(s)
			planCase{private: true, grants: tt.grants}.apply(t, a)
			mustNil(t, a.Refresh(context.Background(), instA, tt.now))
			if got := readEntitlement(t, s, instA).Notice; got != tt.want {
				t.Errorf("Notice = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBudgetMonthKey(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"last second of September", time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC), "llm-budget/2026-09"},
		{"first second of October", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "llm-budget/2026-10"},
		{"non-UTC now", time.Date(2026, 9, 30, 23, 0, 0, 0, time.FixedZone("EST", -5*3600)), "llm-budget/2026-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSpy()
			_, _, b, _ := newTest(s)
			mustNil(t, b.Reserve(context.Background(), 1_000_000, tt.now))
			mustNil(t, b.Settle(context.Background(), 1_000_000, 500_000, tt.now))
			if len(s.calls) != 2 {
				t.Fatalf("calls = %d, want 2", len(s.calls))
			}
			for _, c := range s.calls {
				if c.op != "Add" || c.tenant != store.SystemTenant || c.key != tt.want {
					t.Errorf("call = %s %d %q, want Add 0 %q", c.op, c.tenant, c.key, tt.want)
				}
			}
		})
	}
}
