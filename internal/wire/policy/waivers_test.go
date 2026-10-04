package policy

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWaiversExpiresParsing(t *testing.T) {
	tests := []struct {
		name    string
		expires string // the full `expires` line, "" for an absent key
		want    time.Time
	}{
		{"unquoted date", "expires: 2026-10-20", endOfDay(2026, time.October, 20)},
		{"quoted date", `expires: "2026-10-20"`, endOfDay(2026, time.October, 20)},
		{"leap day", "expires: 2028-02-29", endOfDay(2028, time.February, 29)},
		{"no such leap day", "expires: 2027-02-29", time.Time{}},
		{"short month and day", "expires: 2026-1-5", time.Time{}},
		{"no dashes", "expires: 20261020", time.Time{}},
		{"timestamp", "expires: 2026-10-20T00:00:00Z", time.Time{}},
		{"prose", "expires: next week", time.Time{}},
		{"padded", `expires: " 2026-10-20"`, time.Time{}},
		{"list", "expires: [2026-10-20]", time.Time{}},
		{"empty", "expires:", time.Time{}},
		{"empty string", `expires: ""`, time.Time{}},
		{"absent", "", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A well-formed waiver first: one bad entry fails the whole call.
			policy := "waivers:\n  - rule: r0\n    expires: 2030-01-01\n  - rule: r1\n"
			if tt.expires != "" {
				policy += "    " + tt.expires + "\n"
			}
			got, err := Waivers([]byte(policy))
			if tt.want.IsZero() {
				if err == nil || got != nil {
					t.Fatalf("got %v, %v; want nil and an error", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Waivers: %v", err)
			}
			if len(got) != 2 || !got[1].Expires.Equal(tt.want) || got[1].Expires.Location() != time.UTC {
				t.Errorf("got %+v, want second Expires %v", got, tt.want)
			}
		})
	}
}

func TestWaiversShapes(t *testing.T) {
	day := endOfDay(2026, time.October, 20)
	same := Waiver{Rule: "r", Reason: "same", Expires: day}
	tests := []struct {
		name    string
		in      string
		want    []Waiver
		wantErr bool
	}{
		{name: "no waivers key", in: "version: 2\n"},
		{name: "waivers null", in: "waivers: null\n"},
		{name: "waivers empty list", in: "waivers: []\n"},
		{name: "waivers is a string", in: "waivers: \"none\"\n", wantErr: true},
		{name: "waivers is a list of strings", in: "waivers: [a, b]\n", wantErr: true},
		{name: "waivers is a mapping", in: "waivers:\n  rule: r\n  expires: 2026-10-20\n", wantErr: true},
		{name: "reason is a list", in: "waivers:\n  - reason: [a]\n    expires: 2026-10-20\n", wantErr: true},
		{
			name: "only expires",
			in:   "waivers:\n  - expires: 2026-10-20\n",
			want: []Waiver{{Expires: day}},
		},
		{
			name: "null and non-string scalars",
			in:   "waivers:\n  - rule: 42\n    from: ~\n    to:\n    reason: \"\"\n    expires: 2026-10-20\n",
			want: []Waiver{{Rule: "42", Expires: day}},
		},
		{
			name: "unknown key ticket",
			in:   "waivers:\n  - rule: r\n    ticket: OPS-1\n    expires: 2026-10-20\n",
			want: []Waiver{{Rule: "r", Expires: day}},
		},
		{
			name: "non-chronological order",
			in:   "waivers:\n  - {rule: b, expires: 2027-01-01}\n  - {rule: a, expires: 2026-01-01}\n  - {rule: c, expires: 2026-06-01}\n",
			want: []Waiver{
				{Rule: "b", Expires: endOfDay(2027, time.January, 1)},
				{Rule: "a", Expires: endOfDay(2026, time.January, 1)},
				{Rule: "c", Expires: endOfDay(2026, time.June, 1)},
			},
		},
		{
			name: "identical entries",
			in:   "waivers:\n  - {rule: r, reason: same, expires: 2026-10-20}\n  - {rule: r, reason: same, expires: 2026-10-20}\n",
			want: []Waiver{same, same},
		},
		{
			name: "aliases and merge keys",
			in:   "base: &w {rule: r, reason: same, expires: &day 2026-10-20}\nwaivers:\n  - *w\n  - {<<: *w, rule: r2, expires: *day}\n",
			want: []Waiver{same, {Rule: "r2", Reason: "same", Expires: day}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Waivers([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWaiversIgnoresTheRestOfThePolicy(t *testing.T) {
	var expect struct {
		Fields struct {
			Waivers []struct {
				Rule       string    `json:"rule"`
				From       string    `json:"from"`
				To         string    `json:"to"`
				Reason     string    `json:"reason"`
				ApprovedBy string    `json:"approved_by"`
				Expires    time.Time `json:"expires"`
			} `json:"waivers"`
		} `json:"fields"`
	}
	fixtureJSON(t, fullExpectation, &expect)
	var want []Waiver
	for _, w := range expect.Fields.Waivers {
		want = append(want, Waiver{w.Rule, w.From, w.To, w.Reason, w.ApprovedBy, w.Expires.UTC()})
	}
	if len(want) != 2 {
		t.Fatalf("expectation has %d waivers, want 2", len(want))
	}
	for _, w := range want {
		if w.Rule == "" || w.From == "" || w.To == "" || w.Reason == "" || w.ApprovedBy == "" || w.Expires.IsZero() {
			t.Fatalf("expectation leaves a field empty: %+v", w)
		}
	}

	full := fixture(t, fullPolicy)
	extra := append(bytes.Clone(full), "x_future_engine_key:\n  nested: [1, 2]\n"...)
	for name, policy := range map[string][]byte{"full": full, "extra top-level key": extra} {
		t.Run(name, func(t *testing.T) {
			if got := mustWaivers(t, policy); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestEarliestExpiryIsSchedulable(t *testing.T) {
	expired, active := endOfDay(2026, time.January, 31), endOfDay(2027, time.June, 30)
	for _, name := range []string{"policy/waivers-expired-first.yaml", "policy/waivers-active-first.yaml"} {
		t.Run(name, func(t *testing.T) {
			got := mustWaivers(t, fixture(t, name))
			if len(got) != 2 {
				t.Fatalf("got %d waivers, want both", len(got))
			}
			earliest := slices.MinFunc(got, func(a, b Waiver) int { return a.Expires.Compare(b.Expires) })
			latest := slices.MaxFunc(got, func(a, b Waiver) int { return a.Expires.Compare(b.Expires) })
			if !earliest.Expires.Equal(expired) || !latest.Expires.Equal(active) {
				t.Errorf("earliest %v, latest %v; want %v and %v", earliest.Expires, latest.Expires, expired, active)
			}
		})
	}
}

func TestWaiverIsActiveThroughItsLastDay(t *testing.T) {
	got := mustWaivers(t, []byte("waivers:\n  - rule: r\n    expires: 2026-10-20\n"))
	expires := got[0].Expires
	if lastDay := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC); !lastDay.Before(expires) {
		t.Errorf("%v is not before Expires %v", lastDay, expires)
	}
	if nextDay := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC); !nextDay.After(expires) {
		t.Errorf("%v is not after Expires %v", nextDay, expires)
	}
}

func TestWaiversAsSyntaxCheck(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid policy without waivers", string(fixture(t, realCapturePolicy)), false},
		{"prose", "Here is the policy you asked for. It forbids a to b.\n", true},
		{"truncated", "version: 2\nmodules:\n  a:\n    paths: [pkg/a/**, pkg", true},
		{"unclosed quote", "version: 2\nrules:\n  - id: \"no_direct\n    type: forbidden_dependency\n", true},
		{"duplicate key", "version: 2\nrules: []\nrules: []\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Waivers([]byte(tt.in)); (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestApprovedByIsNeverAuthority(t *testing.T) {
	got := mustWaivers(t, []byte("waivers:\n  - rule: r\n    approved_by: octo-owner\n    expires: 2026-10-20\n"))
	if got[0].ApprovedBy != "octo-owner" {
		t.Errorf("ApprovedBy = %q, want the file's text", got[0].ApprovedBy)
	}

	// The exported surface is the Public Contract and nothing that could act on ApprovedBy.
	want := []string{
		"Accepted", "Baseline", "BaselinePath", "BaselineSchemaV2", "Digest", "LabelsPath", "MaxBaselineBytes",
		"ModuleDef", "Modules", "PolicyPath", "ReadBaseline", "Schema", "Validate", "Waiver", "Waivers",
	}
	if got := exportedNames(t); !slices.Equal(got, want) {
		t.Errorf("exported identifiers = %v, want %v", got, want)
	}
}

func exportedNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				names = append(names, d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names = append(names, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names = append(names, n.Name)
						}
					}
				}
			}
		}
	}
	names = slices.DeleteFunc(names, func(n string) bool { return !ast.IsExported(n) })
	slices.Sort(names)
	return names
}

func TestMalformedExpiryNeedsNoSchedule(t *testing.T) {
	policy := fixture(t, malformedExpiryPolicy)
	if !bytes.Contains(policy, []byte("expires: 2026-13-40")) {
		t.Fatal("fixture lost its malformed expires")
	}
	if got, err := Waivers(policy); err == nil || got != nil {
		t.Errorf("got %v, %v; want nil and an error", got, err)
	}

	var report struct {
		Comparison struct {
			ConfigHash string `json:"config_hash"`
		} `json:"comparison"`
		Findings []struct {
			Status string `json:"status"`
		} `json:"findings"`
	}
	raw, err := os.ReadFile(malformedExpiryReport)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if got := Digest(policy); got != report.Comparison.ConfigHash {
		t.Fatalf("the report is not the run of this policy: Digest = %s, config_hash %s", got, report.Comparison.ConfigHash)
	}
	if len(report.Findings) != 1 || report.Findings[0].Status != "expired_waiver" {
		t.Errorf("report findings = %+v, want the finding already reported as expired_waiver", report.Findings)
	}
}

func FuzzWaivers(f *testing.F) {
	for _, policy := range policyFixtures(f) {
		f.Add(policy)
	}
	f.Add([]byte("base: &w {rule: r, expires: &day 2026-10-20}\nwaivers:\n  - *w\n  - {<<: *w, expires: *day}\n"))
	f.Fuzz(func(t *testing.T, policy []byte) {
		waivers, err := Waivers(policy)
		if err != nil && waivers != nil {
			t.Fatalf("error %v with waivers %v", err, waivers)
		}
		for i, w := range waivers {
			e := w.Expires
			if e.Location() != time.UTC || e.Hour() != 23 || e.Minute() != 59 || e.Second() != 59 || e.Nanosecond() != 0 {
				t.Fatalf("waivers[%d].Expires = %v, want 23:59:59 UTC", i, e)
			}
		}
	})
}
