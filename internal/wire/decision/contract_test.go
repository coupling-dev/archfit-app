package decision_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

func TestSchemaDocument(t *testing.T) {
	var doc struct {
		ID                   string                     `json:"$id"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(decision.Schema(), &doc); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
	if doc.ID != "https://archfit.dev/schemas/decision-request.v1.json" {
		t.Errorf("$id = %q", doc.ID)
	}
	if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Error("additionalProperties must be false")
	}
	var version struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(doc.Properties["schema_version"], &version); err != nil || version.Const != decision.SchemaV1 {
		t.Errorf("schema_version const = %q (%v)", version.Const, err)
	}
	if len(doc.Required) != 9 || len(doc.Properties) != 9 {
		t.Errorf("want 9 required properties and 9 declared, got %d and %d", len(doc.Required), len(doc.Properties))
	}
	if _, ok := doc.Properties["blast_radius"]; ok {
		t.Error("blast_radius is not in v1")
	}
	a, b := decision.Schema(), decision.Schema()
	if !bytes.Equal(a, b) {
		t.Fatal("two calls differ")
	}
	a[0] = 'X'
	if decision.Schema()[0] == 'X' {
		t.Error("Schema returns shared bytes")
	}
}

func TestPackageImportsStdlibOnly(t *testing.T) {
	goList := func(args ...string) []string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...).Output()
		if err != nil {
			t.Fatalf("go list %v: %v", args, err)
		}
		return strings.Fields(string(out))
	}
	const self = "github.com/alexei-led/archfit-app/internal/wire/decision"
	for _, pkg := range goList("-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".") {
		if pkg != self {
			t.Errorf("non-standard-library dependency: %s", pkg)
		}
	}
	for _, pkg := range goList("-f", `{{join .Imports "\n"}}`, ".") {
		switch root, _, _ := strings.Cut(pkg, "/"); root {
		case "net", "os", "log":
			t.Errorf("forbidden import: %s", pkg)
		}
	}
}
