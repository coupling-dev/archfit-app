package envelope_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

func TestDecodeV2FieldRules(t *testing.T) {
	branch244 := strings.Repeat("b", 244)
	tests := []struct {
		name  string
		base  map[string]string
		key   string
		value string // raw JSON; the base value is replaced
		field string // "" = the row must decode
	}{
		{"pull request", prFieldsV2(), "", "", ""},
		{"push", pushFieldsV2("push"), "", "", ""},
		{"merge group", pushFieldsV2("merge_group"), "", "", ""},
		{"re-run", prFieldsV2(), "run_attempt", `2`, ""},
		{"branch with slash and dots", prFieldsV2(), "target_ref", `"refs/heads/release/4.0.x"`, ""},
		{"branch of 244 characters", prFieldsV2(), "target_ref", quoted("refs/heads/" + branch244), ""},
		{"rc engine", prFieldsV2(), "engine_version", `"v4.0.0-rc.1"`, ""},

		{"v1 schema version", prFieldsV2(), "schema_version", `"archfit.report-envelope.v1"`, "schema_version"},
		{"repository without slash", prFieldsV2(), "repository", `"archfit-demo"`, "repository"},
		{"event workflow_dispatch", pushFieldsV2("workflow_dispatch"), "", "", "event"},
		{"event workflow_run", pushFieldsV2("workflow_run"), "", "", "event"},
		{"event empty", pushFieldsV2(""), "", "", "event"},
		{"pull_request event with number 0", prFieldsV2(), "pull_request", `0`, "pull_request"},
		{"push with a number", pushFieldsV2("push"), "pull_request", `7`, "pull_request"},
		{"merge group with a number", pushFieldsV2("merge_group"), "pull_request", `7`, "pull_request"},
		{"negative number", prFieldsV2(), "pull_request", `-1`, "pull_request"},
		{"head_sha uppercase", prFieldsV2(), "head_sha", quoted(strings.ToUpper(hex40)), "head_sha"},
		{"head_sha 39 characters", prFieldsV2(), "head_sha", quoted(hex40[:39]), "head_sha"},
		{"merge_base_sha empty on a push", pushFieldsV2("push"), "merge_base_sha", `""`, "merge_base_sha"},
		{"merge_base_sha empty on a pull request", prFieldsV2(), "merge_base_sha", `""`, "merge_base_sha"},
		{"target_ref short name", prFieldsV2(), "target_ref", `"main"`, "target_ref"},
		{"target_ref tag", prFieldsV2(), "target_ref", `"refs/tags/v4.0.0"`, "target_ref"},
		{"target_ref without branch", prFieldsV2(), "target_ref", `"refs/heads/"`, "target_ref"},
		{"target_ref with a space", prFieldsV2(), "target_ref", `"refs/heads/my branch"`, "target_ref"},
		{"target_ref with a tab", prFieldsV2(), "target_ref", `"refs/heads/my\tbranch"`, "target_ref"},
		{"target_ref not ASCII", prFieldsV2(), "target_ref", `"refs/heads/ветка"`, "target_ref"},
		{"branch of 245 characters", prFieldsV2(), "target_ref", quoted("refs/heads/" + branch244 + "b"), "target_ref"},
		{"target_sha empty", prFieldsV2(), "target_sha", `""`, "target_sha"},
		{"policy_digest bare hex", prFieldsV2(), "policy_digest", quoted(hex64), "policy_digest"},
		{"policy_digest uppercase", prFieldsV2(), "policy_digest", quoted("sha256:" + strings.ToUpper(hex64)), "policy_digest"},
		{"policy_digest sha512", prFieldsV2(), "policy_digest", quoted("sha512:" + hex64), "policy_digest"},
		{"policy_digest empty", prFieldsV2(), "policy_digest", `""`, "policy_digest"},
		{"run_id 0", prFieldsV2(), "run_id", `0`, "run_id"},
		{"run_attempt 0", prFieldsV2(), "run_attempt", `0`, "run_attempt"},
		{"engine_version empty", prFieldsV2(), "engine_version", `""`, "engine_version"},
		{"image_digest bare hex", prFieldsV2(), "image_digest", quoted(hex64), "image_digest"},
		{"platform without arch", prFieldsV2(), "platform", `"linux"`, "platform"},
		{"payload_digest bare hex", prFieldsV2(), "payload_digest", quoted(hex64), "payload_digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := tt.base
			if tt.key != "" {
				fields[tt.key] = tt.value
			}
			raw := renderV2(fields)
			if tt.field == "" {
				wantDecodedV2(t, raw, testPayload)
			} else {
				wantRejectedV2(t, raw, testPayload, tt.field)
			}
			wantSchemaV2Agrees(t, raw, tt.field == "")
		})
	}
}

func TestDecodeV2RejectsTheV1OnlyKeys(t *testing.T) {
	for key, value := range map[string]string{
		"kind":            `"report"`,
		"base_sha":        quoted(hex40),
		"analyzed_sha":    quoted(hex40),
		"integration":     `"merged"`,
		"baseline_digest": `""`,
		"labels_digest":   `""`,
		"policy_change":   `{}`,
	} {
		t.Run(key, func(t *testing.T) {
			fields := prFieldsV2()
			fields[key] = value
			raw := renderV2(fields)
			if msg := wantRejectedV2(t, raw, testPayload, ""); msg != "envelope: unknown key" {
				t.Errorf("error %q, want the unknown key error", msg)
			}
			wantSchemaV2Agrees(t, raw, false)
		})
	}
}

func TestDecodeV2RejectsNonStrictJSON(t *testing.T) {
	valid := renderV2(prFieldsV2())
	withValue := func(key, value string) []byte {
		fields := prFieldsV2()
		fields[key] = value
		return renderV2(fields)
	}
	without := func(key string) []byte {
		fields := prFieldsV2()
		delete(fields, key)
		return renderV2(fields)
	}
	padded := bytes.Replace(valid, []byte("{"), []byte("{"+strings.Repeat(" ", maxRawBytes+1-len(valid))), 1)
	exact := bytes.Replace(valid, []byte("{"), []byte("{"+strings.Repeat(" ", maxRawBytes-len(valid))), 1)

	tests := []struct {
		name  string
		raw   []byte
		field string // the field the error must name; "" = none
	}{
		{"extra key", withValue("installation_id", "1"), ""},
		{"duplicate key", append(bytes.TrimSuffix(valid, []byte("}")), `,"target_sha":"`+hex40+`"}`...), "target_sha"},
		{"case-variant key", bytes.Replace(valid, []byte(`"target_sha"`), []byte(`"Target_SHA"`), 1), ""},
		{"null value", withValue("target_ref", "null"), "target_ref"},
		{"string number", withValue("run_id", `"123"`), "run_id"},
		{"fraction", withValue("run_id", "1.5"), "run_id"},
		{"exponent", withValue("run_id", "1e3"), "run_id"},
		{"run_id above int64", withValue("run_id", "9223372036854775808"), "run_id"},
		{"pull_request above int32", withValue("pull_request", "2147483648"), "pull_request"},
		{"pull_request 7.0", withValue("pull_request", "7.0"), "pull_request"},
		{"trailing object", append(slices.Clone(valid), "{}"...), ""},
		{"top-level array", []byte("[" + string(valid) + "]"), ""},
		{"empty input", nil, ""},
		{"invalid UTF-8", bytes.Replace(valid, []byte("archfit-demo"), []byte("archfit-\xff"), 1), ""},
		{"8 193 bytes", padded, ""},
	}
	for _, key := range fieldOrderV2() {
		tests = append(tests, struct {
			name  string
			raw   []byte
			field string
		}{"absent " + key, without(key), key})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantRejectedV2(t, tt.raw, testPayload, tt.field)
		})
	}
	t.Run("exactly 8 192 bytes", func(t *testing.T) {
		wantDecodedV2(t, exact, testPayload)
	})
}

func TestDecodeV2ErrorsNeverEchoValues(t *testing.T) {
	tests := map[string][]byte{}
	for _, key := range fieldOrderV2() {
		// The space keeps the marker invalid for engine_version and target_ref, whose character sets cover it.
		for name, value := range map[string]string{"string": quoted(leakMarker + " x"), "bare token": leakMarker} {
			fields := prFieldsV2()
			fields[key] = value
			tests[key+" as "+name] = renderV2(fields)
		}
	}
	unknownKey := prFieldsV2()
	unknownKey[leakMarker] = "1"
	tests["unknown key"] = renderV2(unknownKey)
	tests["trailing data"] = append(renderV2(prFieldsV2()), quoted(leakMarker)...)
	tests["duplicate key"] = append(bytes.TrimSuffix(renderV2(prFieldsV2()), []byte("}")), `,"event":"`+leakMarker+`"}`...)

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if msg := wantRejectedV2(t, raw, testPayload, ""); strings.Contains(msg, leakMarker) {
				t.Errorf("error echoes the offending value: %q", msg)
			}
		})
	}
	t.Run("payload", func(t *testing.T) {
		if msg := wantRejectedV2(t, renderV2(prFieldsV2()), []byte(leakMarker), "payload_digest"); strings.Contains(msg, leakMarker) {
			t.Errorf("error echoes the payload: %q", msg)
		}
	})
}

func FuzzDecodeV2(f *testing.F) {
	payload := readFixtureV2(f, standInPayload)
	for _, fx := range loadFixturesV2(f) {
		f.Add(fx.raw)
	}
	fields := pushFieldsV2("push")
	fields["payload_digest"] = quoted(envelope.PayloadDigest(payload))
	f.Add(renderV2(fields))
	f.Add([]byte(`{"run_id":1e3,"event":null,"event":[{}]}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		env, err := envelope.DecodeV2(raw, payload)
		if err != nil {
			if !reflect.DeepEqual(env, envelope.EnvelopeV2{}) {
				t.Fatalf("non-zero EnvelopeV2 with error %v", err)
			}
			return
		}
		again, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		got, err := envelope.DecodeV2(again, payload)
		if err != nil {
			t.Fatalf("re-marshaled envelope does not decode: %v", err)
		}
		if !reflect.DeepEqual(got, env) {
			t.Fatalf("round trip changed the envelope:\n got %+v\nwant %+v", got, env)
		}
	})
}
