package decision_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const (
	hex32 = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	hex40 = "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	hex64 = "5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b"
	leak  = "LEAK-7f3a"
)

func validDoc() map[string]any {
	return map[string]any{
		"schema_version":     decision.SchemaV1,
		"finding_id":         hex32,
		"rule_id":            "boundary.no-import",
		"edge":               map[string]any{"from": "web", "to": "store"},
		"head_sha":           hex40,
		"policy_digest":      hex64,
		"why":                "The web module needs the store read model.\nIt has no other path.",
		"alternatives_tried": []any{"moved the type to a shared module", "added an interface"},
		"proposal":           map[string]any{"kind": "policy_patch", "policy": "version: 1\nmodules:\n  a:\n    paths: [\"pkg/a/**\"]\n"},
	}
}

func render(t testing.TB, doc map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func set(path string, value any) func(map[string]any) {
	return func(doc map[string]any) {
		parent := doc
		keys := strings.Split(path, ".")
		for _, k := range keys[:len(keys)-1] {
			parent = parent[k].(map[string]any)
		}
		if value == nil {
			delete(parent, keys[len(keys)-1])
			return
		}
		parent[keys[len(keys)-1]] = value
	}
}

func TestDecodeAcceptsAValidRequest(t *testing.T) {
	got, err := decision.Decode(render(t, validDoc()))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := decision.Request{
		SchemaVersion: decision.SchemaV1, FindingID: hex32, RuleID: "boundary.no-import",
		Edge: decision.Edge{From: "web", To: "store"}, HeadSHA: hex40, PolicyDigest: hex64,
		Why:               "The web module needs the store read model.\nIt has no other path.",
		AlternativesTried: []string{"moved the type to a shared module", "added an interface"},
		Proposal:          decision.Proposal{Kind: "policy_patch", Policy: "version: 1\nmodules:\n  a:\n    paths: [\"pkg/a/**\"]\n"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecodeFieldRules(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(map[string]any)
		field       string // "" = decodes
		decoderOnly bool   // a rule the schema cannot express
	}{
		{"valid", func(map[string]any) {}, "", false},
		{"wrong schema_version", set("schema_version", "archfit.decision-request.v2"), "schema_version", false},
		{"finding_id uppercase", set("finding_id", strings.ToUpper(hex32)), "finding_id", false},
		{"finding_id 31", set("finding_id", hex32[:31]), "finding_id", false},
		{"finding_id 33", set("finding_id", hex32+"a"), "finding_id", false},
		{"finding_id empty", set("finding_id", ""), "finding_id", false},
		{"rule_id empty", set("rule_id", ""), "rule_id", false},
		{"rule_id with space", set("rule_id", "a b"), "rule_id", false},
		{"rule_id 128", set("rule_id", strings.Repeat("r", 128)), "", false},
		{"rule_id 129", set("rule_id", strings.Repeat("r", 129)), "rule_id", false},
		{"edge.from empty", set("edge.from", ""), "edge.from", false},
		{"edge.to with space", set("edge.to", "a b"), "edge.to", false},
		{"edge.to 200", set("edge.to", strings.Repeat("m", 200)), "", false},
		{"edge.to 201", set("edge.to", strings.Repeat("m", 201)), "edge.to", false},
		{"head_sha 39", set("head_sha", hex40[:39]), "head_sha", false},
		{"head_sha uppercase", set("head_sha", strings.ToUpper(hex40)), "head_sha", false},
		{"policy_digest 63", set("policy_digest", hex64[:63]), "policy_digest", false},
		{"policy_digest prefixed", set("policy_digest", "sha256:"+hex64), "policy_digest", false},
		{"why empty", set("why", ""), "why", false},
		{"why 2000", set("why", strings.Repeat("w", 2000)), "", false},
		{"why 2001", set("why", strings.Repeat("w", 2001)), "why", false},
		{"why 2000 multibyte characters", set("why", strings.Repeat("é", 2000)), "", false},
		{"why newline and tab", set("why", "a\n\tb"), "", false},
		{"why NUL", set("why", "a\x00b"), "why", false},
		{"why CR", set("why", "a\r\nb"), "why", false},
		{"why ESC", set("why", "a\x1bb"), "why", false},
		{"why DEL", set("why", "a\x7fb"), "why", false},
		{"why C1", set("why", "a\u0085b"), "why", false},
		{"why U+2028", set("why", "a\u2028b"), "why", true},
		{"alternatives empty list", set("alternatives_tried", []any{}), "", false},
		{"alternatives 20", set("alternatives_tried", repeated("x", 20)), "", false},
		{"alternatives 21", set("alternatives_tried", repeated("x", 21)), "alternatives_tried", false},
		{"alternative empty", set("alternatives_tried", []any{""}), "alternatives_tried", false},
		{"alternative 500", set("alternatives_tried", []any{strings.Repeat("a", 500)}), "", false},
		{"alternative 501", set("alternatives_tried", []any{strings.Repeat("a", 501)}), "alternatives_tried", false},
		{"alternative control", set("alternatives_tried", []any{"a\x01"}), "alternatives_tried", false},
		{"proposal kind other", set("proposal.kind", "baseline_patch"), "proposal.kind", false},
		{"policy empty", set("proposal.policy", ""), "proposal.policy", false},
		{"policy 65536", set("proposal.policy", strings.Repeat("a", 65536)), "", false},
		{"policy 65537", set("proposal.policy", strings.Repeat("a", 65537)), "proposal.policy", false},
		{"policy control", set("proposal.policy", "a\x01"), "proposal.policy", false},
		{"policy over 64 KiB in bytes", set("proposal.policy", strings.Repeat("€", 21846)), "proposal.policy", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDoc()
			tt.mutate(doc)
			raw := render(t, doc)
			_, err := decision.Decode(raw)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), "decision: "+tt.field+": ") {
				t.Fatalf("error = %v, want one naming %s", err, tt.field)
			}
			if !tt.decoderOnly {
				wantSchemaAgrees(t, raw, err == nil)
			}
		})
	}
}

func repeated(s string, n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestDecodeRejectsNonStrictJSON(t *testing.T) {
	good := string(render(t, validDoc()))
	tests := []struct {
		name        string
		raw         string
		decoderOnly bool
	}{
		{"extra top-level key", strings.Replace(good, `{`, `{"blast_radius":{"modules":1,"edges":1},`, 1), false},
		{"extra edge key", strings.Replace(good, `"edge":{`, `"edge":{"weight":1,`, 1), false},
		{"extra proposal key", strings.Replace(good, `"proposal":{`, `"proposal":{"extra":1,`, 1), false},
		{"duplicate head_sha", strings.Replace(good, `{`, `{"head_sha":"`+hex40+`",`, 1), true},
		{"duplicate edge.from", strings.Replace(good, `"edge":{`, `"edge":{"from":"web",`, 1), true},
		{"case variant key", strings.Replace(good, `"head_sha"`, `"Head_SHA"`, 1), false},
		{"alternatives with a null", strings.Replace(good, `["moved`, `[null,"moved`, 1), false},
		{"number in alternatives", strings.Replace(good, `["moved`, `[1,"moved`, 1), false},
		{"nested array in alternatives", strings.Replace(good, `["moved`, `[["x"],"moved`, 1), false},
		{"alternatives is a string", strings.Replace(good, `["moved the type to a shared module","added an interface"]`, `"x"`, 1), false},
		{"edge is a string", strings.Replace(good, `{"from":"web","to":"store"}`, `"web"`, 1), false},
		{"proposal is an array", strings.Replace(good, `"proposal":{`, `"proposal":[{`, 1), false},
		{"finding_id is a number", strings.Replace(good, `"`+hex32+`"`, `7`, 1), false},
		{"trailing object", good + `{}`, false},
		{"top-level array", `[` + good + `]`, false},
		{"empty input", ``, false},
		{"truncated", good[:len(good)/2], false},
		{"invalid UTF-8", strings.Replace(good, `web`, "w\xffb", 1), true},
		{"over the raw cap", `{"why":"` + strings.Repeat("a", 1<<18) + `"}`, true},
	}
	for key := range validDoc() {
		doc := validDoc()
		doc[key] = nil
		tests = append(tests, struct {
			name        string
			raw         string
			decoderOnly bool
		}{"null " + key, string(render(t, doc)), false})
		doc = validDoc()
		delete(doc, key)
		tests = append(tests, struct {
			name        string
			raw         string
			decoderOnly bool
		}{"absent " + key, string(render(t, doc)), false})
	}
	for _, key := range []string{"edge.from", "edge.to", "proposal.kind", "proposal.policy"} {
		doc := validDoc()
		set(key, nil)(doc)
		tests = append(tests, struct {
			name        string
			raw         string
			decoderOnly bool
		}{"absent " + key, string(render(t, doc)), false})
		doc = validDoc()
		parent, name, _ := strings.Cut(key, ".")
		doc[parent].(map[string]any)[name] = nil
		tests = append(tests, struct {
			name        string
			raw         string
			decoderOnly bool
		}{"null " + key, string(render(t, doc)), false})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decision.Decode([]byte(tt.raw))
			if err == nil {
				t.Fatal("Decode accepted it")
			}
			if strings.HasPrefix(tt.name, "null ") && !strings.Contains(err.Error(), "must not be null") {
				t.Errorf("error = %v, want the null named", err)
			}
			if !reflect.DeepEqual(got, decision.Request{}) {
				t.Errorf("non-zero Request with an error")
			}
			if !tt.decoderOnly {
				wantSchemaAgrees(t, []byte(tt.raw), false)
			}
		})
	}
}

func TestErrorsNeverEchoValues(t *testing.T) {
	bad := map[string]func(map[string]any){
		"schema_version":  set("schema_version", leak),
		"finding_id":      set("finding_id", leak),
		"rule_id":         set("rule_id", leak+" "),
		"edge.from":       set("edge.from", leak+" "),
		"head_sha":        set("head_sha", leak),
		"policy_digest":   set("policy_digest", leak),
		"why":             set("why", leak+"\x01"),
		"alternatives":    set("alternatives_tried", []any{leak + "\x01"}),
		"proposal.kind":   set("proposal.kind", leak),
		"proposal.policy": set("proposal.policy", leak+"\x01"),
		"unknown key":     func(d map[string]any) { d[leak] = 1 },
		"wrong type":      set("why", map[string]any{leak: 1}),
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			doc := validDoc()
			mutate(doc)
			_, err := decision.Decode(render(t, doc))
			if err == nil {
				t.Fatal("Decode accepted it")
			}
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error echoes a value: %v", err)
			}
		})
	}
	_, err := decision.Decode([]byte(`{"` + leak + `":1,"` + leak + `":2}`))
	if err == nil || strings.Contains(err.Error(), leak) {
		t.Errorf("error = %v", err)
	}
}

func TestIdentify(t *testing.T) {
	tests := []struct {
		name, raw, finding, digest string
	}{
		{"both valid", `{"finding_id":"` + hex32 + `","policy_digest":"` + hex64 + `"}`, hex32, hex64},
		{"bad digest", `{"finding_id":"` + hex32 + `","policy_digest":"x"}`, hex32, decision.Unknown},
		{"bad finding", `{"finding_id":"` + leak + `","policy_digest":"` + hex64 + `"}`, decision.Unknown, hex64},
		{"not JSON", `nope`, decision.Unknown, decision.Unknown},
		{"wrong types", `{"finding_id":1,"policy_digest":null}`, decision.Unknown, decision.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, d := decision.Identify([]byte(tt.raw))
			if f != tt.finding || d != tt.digest {
				t.Errorf("Identify = %q %q, want %q %q", f, d, tt.finding, tt.digest)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(render(f, validDoc()))
	f.Add([]byte(`{"finding_id":null,"finding_id":[{}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		req, err := decision.Decode(raw)
		if err != nil {
			if !reflect.DeepEqual(req, decision.Request{}) {
				t.Fatalf("non-zero Request with error %v", err)
			}
			return
		}
		again, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decision.Decode(again)
		if err != nil || !reflect.DeepEqual(got, req) {
			t.Fatalf("re-marshaled request differs: %v", err)
		}
	})
}

func TestExtract(t *testing.T) {
	body := string(render(t, validDoc()))
	block := decision.OpenMarker + "\n```json\n" + body + "\n```\n" + decision.CloseMarker
	tests := []struct {
		name    string
		comment string
		wantErr string // "" = the JSON; "none" = ErrNoRequest; otherwise a malformed block
	}{
		{"block", block, ""},
		{"block with prose around it", "Please decide.\n\n" + block + "\n\nThanks", ""},
		{"CRLF", strings.ReplaceAll(block, "\n", "\r\n"), ""},
		{"no block", "LGTM", "none"},
		{"empty comment", "", "none"},
		{"close marker only", decision.CloseMarker, "none"},
		{"two blocks", block + "\n" + block, "more than one request block"},
		{"two ends", block + decision.CloseMarker, "more than one request block end"},
		{"two starts, one end", decision.OpenMarker + "\n" + block, "more than one request block"},
		{"not closed", decision.OpenMarker + "\n```json\n" + body + "\n```", "not closed"},
		{"closed before opened", decision.CloseMarker + decision.OpenMarker, "ends before it begins"},
		{"no fence", decision.OpenMarker + "\n" + body + "\n" + decision.CloseMarker, "```json fence"},
		{"fence without language", decision.OpenMarker + "\n```\n" + body + "\n```\n" + decision.CloseMarker, "```json fence"},
		{"language glued on", decision.OpenMarker + "\n```jsonc\n" + body + "\n```\n" + decision.CloseMarker, "```json fence"},
		{"fence not closed", decision.OpenMarker + "\n```json\n" + body + "\n" + decision.CloseMarker, "```json fence"},
		{"block past the scan cap", strings.Repeat("a", 1<<18) + block, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := decision.Extract(tt.comment)
			switch tt.wantErr {
			case "":
				if err != nil {
					t.Fatalf("Extract: %v", err)
				}
				if _, err := decision.Decode(raw); err != nil {
					t.Fatalf("Decode of the extracted JSON: %v", err)
				}
			case "none":
				if !errors.Is(err, decision.ErrNoRequest) {
					t.Fatalf("error = %v, want ErrNoRequest", err)
				}
			default:
				if err == nil || errors.Is(err, decision.ErrNoRequest) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want a malformed block: %s", err, tt.wantErr)
				}
			}
		})
	}
}

func TestHasRequest(t *testing.T) {
	if !decision.HasRequest("x "+decision.OpenMarker) || decision.HasRequest("LGTM") || decision.HasRequest(decision.CloseMarker) {
		t.Error("HasRequest does not follow the open marker")
	}
}

func FuzzExtract(f *testing.F) {
	f.Add(decision.OpenMarker + "\n```json\n{}\n```\n" + decision.CloseMarker)
	f.Fuzz(func(t *testing.T, comment string) {
		if raw, err := decision.Extract(comment); err == nil && !bytes.Contains([]byte(comment), raw) {
			t.Fatal("extracted bytes are not in the comment")
		}
	})
}

func TestStatusMarkerAndLine(t *testing.T) {
	if got, want := decision.StatusMarker(hex32, hex64), "<!-- archfit:decision-status "+hex32+" "+hex64+" -->"; got != want {
		t.Errorf("StatusMarker = %q, want %q", got, want)
	}
	tests := []struct {
		status decision.Status
		detail string
		want   string
	}{
		{decision.StatusPending, "", "pending"},
		{decision.StatusApproved, decision.ApprovedDetail(7), "approved (proposal PR #7)"},
		{decision.StatusDeclined, "", "declined"},
		{decision.StatusDeclined, "why: rule", "declined (why: rule)"},
		{decision.StatusStale, decision.ReasonHeadMoved, "stale (head moved)"},
		{decision.StatusStale, decision.ReasonPolicyChanged, "stale (policy changed)"},
		{decision.StatusStale, decision.ReasonPullClosed, "stale (pull request closed)"},
	}
	for _, tt := range tests {
		if got := decision.StatusLine(tt.status, tt.detail); got != tt.want {
			t.Errorf("StatusLine(%s, %q) = %q, want %q", tt.status, tt.detail, got, tt.want)
		}
	}
}
