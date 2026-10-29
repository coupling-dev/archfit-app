package settings

import (
	"reflect"
	"strings"
	"testing"
)

func withIntegration(text string) []byte {
	return append(baseWith("", ""), []byte(text)...)
}

func TestParseIntegration(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		want     *Integration
		wantMode RecheckMode
	}{
		{"absent", "", nil, RecheckOverlap},
		{"overlap", "integration:\n  recheck: overlap\n", &Integration{Recheck: RecheckOverlap}, RecheckOverlap},
		{"off", "integration:\n  recheck: off\n", &Integration{Recheck: RecheckOff}, RecheckOff},
		{"empty mapping", "integration: {}\n", &Integration{Recheck: RecheckOverlap}, RecheckOverlap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(withIntegration(tt.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got.Integration, tt.want) || got.Recheck() != tt.wantMode {
				t.Errorf("Integration = %+v, Recheck() = %q; want %+v, %q", got.Integration, got.Recheck(), tt.want, tt.wantMode)
			}
			again, err := Parse(Render(got))
			if err != nil || !reflect.DeepEqual(again, got) {
				t.Errorf("round trip: %+v, %v; want %+v", again, err, got)
			}
		})
	}
}

func TestIntegrationRules(t *testing.T) {
	tests := []struct {
		name, text, wantKey string
	}{
		{"scalar", "integration: overlap\n", "integration"},
		{"null", "integration:\n", "integration"},
		{"unknown key", "integration:\n  recheck: off\n  repair: true\n", "integration"},
		{"only an unknown key", "integration:\n  repair: off\n", "integration"},
		{"duplicate key", "integration:\n  recheck: off\n  recheck: overlap\n", "integration.recheck"},
		{"other case", "integration:\n  recheck: Off\n", "integration.recheck"},
		{"unknown mode", "integration:\n  recheck: always\n", "integration.recheck"},
		{"boolean", "integration:\n  recheck: true\n", "integration.recheck"},
		{"empty value", "integration:\n  recheck:\n", "integration.recheck"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(withIntegration(tt.text))
			if err == nil {
				t.Fatal("Parse accepted it")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q does not name %q", err, tt.wantKey)
			}
		})
	}
}
