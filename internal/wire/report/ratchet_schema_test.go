package report

import (
	"reflect"
	"testing"
)

func TestRatchetSchemaRetainsEveryPublishedConstraint(t *testing.T) {
	published := parseDoc(t, loadFixture(t, "schema/archfit.state.schema.json"))
	candidate := parseDoc(t, loadFixture(t, "schema-candidate/archfit.state.schema.json"))
	assertSchemaRetained(t, "schema", published, candidate)
}

func assertSchemaRetained(t *testing.T, path string, published, candidate any) {
	t.Helper()
	old, isObject := published.(map[string]any)
	if !isObject {
		if !reflect.DeepEqual(published, candidate) {
			t.Errorf("%s changed a published constraint: %v → %v", path, published, candidate)
		}
		return
	}
	current, ok := candidate.(map[string]any)
	if !ok {
		t.Fatalf("%s lost a published schema object", path)
	}
	for key, value := range old {
		if key == "description" || key == "$comment" {
			continue
		}
		got, exists := current[key]
		if !exists {
			t.Errorf("%s.%s removed a published constraint", path, key)
			continue
		}
		assertSchemaRetained(t, path+"."+key, value, got)
	}
}
