package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSchemaIsThePinnedEngineFile(t *testing.T) {
	raw, err := os.ReadFile("SCHEMA_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	source := map[string]string{}
	for line := range strings.Lines(string(raw)) {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ": "); ok {
			source[key] = value
		}
	}
	if got, want := source["revision"], "c7490636480a8faedddf3bff1d0a678a7ab7987c"; got != want {
		t.Errorf("SCHEMA_SOURCE revision = %q, want %q", got, want)
	}
	if got, want := source["path"], "archfit.schema.json"; got != want {
		t.Errorf("SCHEMA_SOURCE path = %q, want %q", got, want)
	}
	const pinned = "37a4b6d65a9ba8dcdf3e9eb4d2ce5338e8685e249b555faf201a54b8c4dad32e"
	sum := sha256.Sum256(Schema())
	if got := hex.EncodeToString(sum[:]); got != pinned || source["sha256"] != pinned {
		t.Errorf("Schema() sha256 = %s, SCHEMA_SOURCE says %q, want %s", got, source["sha256"], pinned)
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]struct {
			Required []string `json:"required"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(Schema(), &schema); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
	for _, key := range []string{"waivers", "modules"} {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("schema has no %q property", key)
		}
	}
	waiver, ok := schema.Defs["WaiverDef"]
	if !ok {
		t.Fatal("schema has no $defs.WaiverDef")
	}
	for _, key := range []string{"rule", "reason", "approved_by", "expires"} {
		if !slices.Contains(waiver.Required, key) {
			t.Errorf("WaiverDef does not require %q", key)
		}
	}

	first, second := Schema(), Schema()
	first[0] ^= 0xff
	if bytes.Equal(first, second) || !bytes.Equal(second, Schema()) {
		t.Error("Schema() calls share their bytes")
	}
}
