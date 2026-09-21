package settings

import (
	"bytes"

	"go.yaml.in/yaml/v3"
)

const header = "# Archfit App settings. Changes need architecture-owner approval.\n"

// Render returns canonical YAML for s. For any s returned by Parse, Parse(Render(s)) == s.
func Render(s Settings) []byte {
	if s.Gate == "" {
		s.Gate = GateAdvisory
	}
	if s.DataMode == "" {
		s.DataMode = DataCustomer
	}

	var out bytes.Buffer
	out.WriteString(header)
	// The encoder quotes an owner such as "123" or "true", which Parse would otherwise reject as not a string.
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if enc.Encode(s) != nil || enc.Close() != nil {
		// Not reachable for a struct of strings and an int. The header alone fails Parse, so callers fail closed.
		return []byte(header)
	}
	return out.Bytes()
}
