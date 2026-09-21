package envelope_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

const (
	maxRawBytes = 8192
	leakMarker  = "LEAK-7f3a"
)

func quoted(s string) string { return `"` + s + `"` }

func TestDecodeFieldRules(t *testing.T) {
	t.Run("unmodified base decodes", func(t *testing.T) {
		got := wantDecoded(t, render(prFields()))
		want := envelope.Envelope{
			SchemaVersion: envelope.SchemaV1,
			Kind:          envelope.KindReport,
			Repository:    "alexei-led/archfit-demo",
			Event:         "pull_request",
			PullRequest:   42,
			HeadSHA:       hex40,
			BaseSHA:       hex40,
			MergeBaseSHA:  hex40,
			RunID:         17654321098,
			RunAttempt:    1,
			EngineVersion: "v2.3.1",
			ImageDigest:   "sha256:" + hex64,
			Platform:      "linux/amd64",
			PayloadDigest: "sha256:" + hex64,
		}
		if got != want {
			t.Errorf("got %+v\nwant %+v", got, want)
		}
	})

	tests := []struct {
		name  string
		field string
		value string
	}{
		{"wrong schema_version", "schema_version", quoted("archfit.report-envelope.v2")},
		{"kind state", "kind", quoted("state")},
		{"repository without slash", "repository", quoted("ownername")},
		{"repository with two slashes", "repository", quoted("owner/name/extra")},
		{"repository with empty owner", "repository", quoted("/name")},
		{"event with space and capitals", "event", quoted("Pull Request")},
		{"head_sha uppercase", "head_sha", quoted(strings.ToUpper(hex40))},
		{"head_sha 39 characters", "head_sha", quoted(hex40[:39])},
		{"head_sha 41 characters", "head_sha", quoted(hex40 + "0")},
		{"image_digest without prefix", "image_digest", quoted(hex64)},
		{"image_digest sha512", "image_digest", quoted("sha512:" + hex64)},
		{"image_digest 63 hex", "image_digest", quoted("sha256:" + hex64[:63])},
		{"payload_digest uppercase", "payload_digest", quoted("sha256:" + strings.ToUpper(hex64))},
		{"run_id zero", "run_id", "0"},
		{"run_id negative", "run_id", "-1"},
		{"run_attempt zero", "run_attempt", "0"},
		{"engine_version empty", "engine_version", quoted("")},
		{"engine_version 65 characters", "engine_version", quoted(strings.Repeat("v", 65))},
		{"platform without slash", "platform", quoted("linux")},
		{"platform 33 characters", "platform", quoted("linux/" + strings.Repeat("a", 27))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := prFields()
			fields[tt.field] = tt.value
			raw := render(fields)
			wantRejected(t, raw, tt.field)
			wantSchemaAgrees(t, raw, false)
		})
	}
}

func TestDecodeCrossFieldRules(t *testing.T) {
	with := func(fields map[string]string, key, value string) map[string]string {
		fields[key] = value
		return fields
	}
	tests := []struct {
		name      string
		fields    map[string]string
		wantField string // "" = decodes
	}{
		{"pull_request event with pull_request 0", with(prFields(), "pull_request", "0"), "pull_request"},
		{"push with pull_request 7", with(pushFields("push"), "pull_request", "7"), "pull_request"},
		{"push with base_sha", with(pushFields("push"), "base_sha", quoted(hex40)), "base_sha"},
		{"pull_request event with empty merge_base_sha", with(prFields(), "merge_base_sha", quoted("")), "merge_base_sha"},
		{"valid push", pushFields("push"), ""},
		{"valid workflow_dispatch", pushFields("workflow_dispatch"), ""},
		{"valid pull_request", prFields(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := render(tt.fields)
			if tt.wantField == "" {
				wantDecoded(t, raw)
			} else {
				wantRejected(t, raw, tt.wantField)
			}
			wantSchemaAgrees(t, raw, tt.wantField == "")
		})
	}
}

func TestInputDigestFormats(t *testing.T) {
	const absent = "<absent>"
	tests := []struct {
		name   string
		base   func() map[string]string
		value  string
		wantOK bool
	}{
		{"64 lowercase hex", prFields, quoted(hex64), true},
		{"empty", prFields, quoted(""), true},
		{"sha256 prefix", prFields, quoted("sha256:" + hex64), false},
		{"uppercase hex", prFields, quoted(strings.ToUpper(hex64)), false},
		{"63 hex", prFields, quoted(hex64[:63]), false},
		{"key absent", prFields, absent, false},
		{"null", prFields, "null", false},
		{"non-empty with kind discovery", discoveryFields, quoted(hex64), false},
	}
	for _, field := range []string{"baseline_digest", "labels_digest"} {
		for _, tt := range tests {
			t.Run(field+"/"+tt.name, func(t *testing.T) {
				fields := tt.base()
				fields[field] = tt.value
				if tt.value == absent {
					delete(fields, field)
				}
				raw := render(fields)
				if tt.wantOK {
					wantDecoded(t, raw)
				} else {
					wantRejected(t, raw, field)
				}
				wantSchemaAgrees(t, raw, tt.wantOK)
			})
		}
	}

	t.Run("payload_digest/bare 64 hex", func(t *testing.T) {
		fields := prFields()
		fields["payload_digest"] = quoted(hex64)
		raw := render(fields)
		wantRejected(t, raw, "payload_digest")
		wantSchemaAgrees(t, raw, false)
	})
}

// The schema cannot see a number's spelling or the int64 range, so no schema agreement is checked here:
// draft 2020-12 treats 7.0 and 1e3 as integers. The decoder is the stricter of the two.
func TestNumbersMustBeIntegers(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{"run_id fraction", "run_id", "1.5"},
		{"run_id exponent", "run_id", "1e3"},
		{"run_id string", "run_id", `"123"`},
		{"run_id above int64", "run_id", "9223372036854775808"},
		{"pull_request integral fraction", "pull_request", "7.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := prFields()
			fields[tt.field] = tt.value
			wantRejected(t, render(fields), tt.field)
		})
	}
}

func TestDecodeRejectsNonStrictJSON(t *testing.T) {
	valid := render(prFields())
	change := func(edit func(fields map[string]string)) []byte {
		fields := prFields()
		edit(fields)
		return render(fields)
	}
	padTo := func(size int) []byte {
		return append(bytes.Clone(valid), bytes.Repeat([]byte(" "), size-len(valid))...)
	}

	tests := []struct {
		name string
		raw  []byte
	}{
		{"extra key installation_id", change(func(f map[string]string) { f["installation_id"] = "7" })},
		{"duplicate head_sha", append(bytes.TrimSuffix(bytes.Clone(valid), []byte("}")), `,"head_sha":"`+hex40+`"}`...)},
		{"case-variant key Head_SHA", change(func(f map[string]string) {
			f["Head_SHA"] = f["head_sha"]
			delete(f, "head_sha")
		})},
		{"kind null", change(func(f map[string]string) { f["kind"] = "null" })},
		{"trailing object", append(bytes.Clone(valid), "{}"...)},
		{"top-level array", append(append([]byte("["), valid...), ']')},
		{"empty input", nil},
		{"whitespace only", []byte("  \n")},
		{"invalid UTF-8", change(func(f map[string]string) { f["repository"] = "\"own\xffer/name\"" })},
		{"8193 bytes", padTo(maxRawBytes + 1)},
		{"nested value", change(func(f map[string]string) { f["platform"] = `{"os":"linux"}` })},
		{"truncated object", valid[:len(valid)-1]},
	}
	for _, key := range fieldOrder() {
		tests = append(tests, struct {
			name string
			raw  []byte
		}{"absent " + key, change(func(f map[string]string) { delete(f, key) })})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantRejected(t, tt.raw, "")
		})
	}

	t.Run("8192 bytes is the limit", func(t *testing.T) {
		if _, err := envelope.Decode(padTo(maxRawBytes)); err != nil {
			t.Errorf("an envelope of exactly %d bytes must decode: %v", maxRawBytes, err)
		}
	})
}

func TestErrorsNeverEchoValues(t *testing.T) {
	tests := map[string][]byte{}
	for _, key := range fieldOrder() {
		// The space keeps the marker invalid for engine_version too, whose character set covers the marker itself.
		for name, value := range map[string]string{"string": quoted(leakMarker + " x"), "bare token": leakMarker} {
			fields := prFields()
			fields[key] = value
			tests[key+" as "+name] = render(fields)
		}
	}
	unknownKey := prFields()
	unknownKey[leakMarker] = "1"
	tests["unknown key"] = render(unknownKey)
	tests["trailing data"] = append(render(prFields()), quoted(leakMarker)...)
	tests["duplicate key"] = append(bytes.TrimSuffix(render(prFields()), []byte("}")), `,"kind":"`+leakMarker+`"}`...)

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if msg := wantRejected(t, raw, ""); strings.Contains(msg, leakMarker) {
				t.Errorf("error echoes the offending value: %q", msg)
			}
		})
	}

	t.Run("number literal", func(t *testing.T) {
		fields := prFields()
		fields["run_id"] = "73571.25"
		if msg := wantRejected(t, render(fields), "run_id"); strings.Contains(msg, "73571") {
			t.Errorf("error echoes the offending value: %q", msg)
		}
	})
}

func FuzzDecode(f *testing.F) {
	for _, fx := range loadFixtures(f) {
		f.Add(fx.raw)
	}
	f.Add(render(pushFields("push")))
	f.Add([]byte(`{"run_id":1e3,"kind":null,"kind":[{}]}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		env, err := envelope.Decode(raw)
		if err != nil {
			if env != (envelope.Envelope{}) {
				t.Fatalf("non-zero Envelope with error %v", err)
			}
			return
		}
		again, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		got, err := envelope.Decode(again)
		if err != nil {
			t.Fatalf("re-marshaled envelope does not decode: %v", err)
		}
		if got != env {
			t.Fatalf("round trip changed the envelope:\n got %+v\nwant %+v", got, env)
		}
	})
}
