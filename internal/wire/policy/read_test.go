package policy

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

func aliasBomb() string {
	var b strings.Builder
	b.WriteString("a: &a [x, x, x, x, x, x, x, x, x]\n")
	for c := 'b'; c <= 'j'; c++ {
		prev := string(c - 1)
		fmt.Fprintf(&b, "%c: &%c [%s]\n", c, c, strings.TrimSuffix(strings.Repeat("*"+prev+", ", 9), ", "))
	}
	b.WriteString("waivers: *j\nmodules: *j\n")
	return b.String()
}

func TestReadersRejectBadInput(t *testing.T) {
	readers := []struct {
		name string
		read func([]byte) (int, error)
	}{
		{"Waivers", func(p []byte) (int, error) { got, err := Waivers(p); return len(got), err }},
		{"Modules", func(p []byte) (int, error) { got, err := Modules(p); return len(got), err }},
	}
	atLimit := "modules:\n  a: {}\nwaivers:\n  - expires: 2026-10-20\n"
	atLimit += "#" + strings.Repeat("x", 1<<20-len(atLimit)-1)
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "invalid YAML", in: "modules: [a\nwaivers: }\n", wantErr: true},
		{name: "invalid UTF-8", in: "version: 2\nnote: \xff\xfe\n", wantErr: true},
		{name: "UTF-16", in: "\xff\xfev\x00:\x00 \x002\x00\n\x00", wantErr: true},
		{name: "two documents", in: "version: 2\n---\nversion: 2\n", wantErr: true},
		{name: "second document is broken", in: "version: 2\n---\n[a\n", wantErr: true},
		{name: "root is a list", in: "- modules\n- waivers\n", wantErr: true},
		{name: "root is a scalar", in: "null\n", wantErr: true},
		{name: "over the input limit", in: atLimit + "x", wantErr: true},
		{name: "alias-expansion bomb", in: aliasBomb(), wantErr: true},
		{name: "complex key", in: "? [a, b]\n: c\n", wantErr: true},

		{name: "empty input"},
		{name: "comments only", in: "# nothing here\n"},
		{name: "at the input limit", in: atLimit, want: 1},
		{name: "explicit document markers", in: "---\nmodules: {a: {}}\nwaivers: [{expires: 2026-10-20}]\n...\n", want: 1},
	}
	for _, r := range readers {
		for _, tt := range tests {
			t.Run(r.name+"/"+tt.name, func(t *testing.T) {
				got, err := r.read([]byte(tt.in))
				if (err != nil) != tt.wantErr {
					t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
				}
				if got != tt.want {
					t.Errorf("got %d entries, want %d", got, tt.want)
				}
			})
		}
	}
}

func TestErrorsNeverEchoValues(t *testing.T) {
	const marker = "LEAK-7f3a"
	tests := []struct {
		name     string
		read     func([]byte) error
		in       string
		wantText []string
	}{
		{
			name:     "malformed expires",
			read:     func(p []byte) error { _, err := Waivers(p); return err },
			in:       "waivers:\n  - rule: r0\n    expires: 2026-10-20\n  - rule: r1\n    reason: " + marker + "\n    expires: 2026-" + marker + "\n",
			wantText: []string{"waivers[1]", "expires"},
		},
		{
			name:     "non-scalar reason",
			read:     func(p []byte) error { _, err := Waivers(p); return err },
			in:       "waivers:\n  - reason: [" + marker + "]\n    expires: 2026-10-20\n",
			wantText: []string{"waivers[0]", "reason"},
		},
		{
			name:     "non-scalar layer",
			read:     func(p []byte) error { _, err := Modules(p); return err },
			in:       "modules:\n  a: {}\n  b:\n    layer: [" + marker + "]\n",
			wantText: []string{"modules[1]", "layer"},
		},
		{
			name: "syntax error",
			read: func(p []byte) error { _, err := Waivers(p); return err },
			in:   "reason: \"" + marker + "\nwaivers: [*" + marker + "]\n",
		},
		{
			name: "duplicate key",
			read: func(p []byte) error { _, err := Waivers(p); return err },
			in:   marker + ": 1\n" + marker + ": 2\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.read([]byte(tt.in))
			if err == nil {
				t.Fatal("no error")
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("error echoes the input: %v", err)
			}
			for _, text := range tt.wantText {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("error %q does not name %q", err, text)
				}
			}
		})
	}
}

func TestReadBoundsTheLibraryDecode(t *testing.T) {
	repeat := func(n int, line func(i int) string) string {
		var b strings.Builder
		b.WriteString("version: 2\nmodules:\n")
		for i := range n {
			b.WriteString(line(i))
		}
		return b.String()
	}
	var aliased strings.Builder
	aliased.WriteString(repeat(5000, func(i int) string { return fmt.Sprintf("  m%d: {}\n", i) }))
	aliased.WriteString("layers: &big {")
	for i := range 2000 {
		fmt.Fprintf(&aliased, "k%d: 1, ", i)
	}
	aliased.WriteString("}\nexclude: [")
	for range 200 {
		aliased.WriteString("*big, ")
	}
	aliased.WriteString("]\n")

	tests := []struct {
		name string
		in   string
		want error
	}{
		{"50 000 identical keys", repeat(50_000, func(int) string { return "  a: {}\n" }), errSyntax},
		{"too many keys in one mapping", repeat(maxMappingKeys+1, func(i int) string { return fmt.Sprintf("  m%d: {}\n", i) }), errTooComplex},
		{"a large mapping expanded by many aliases", aliased.String(), errTooComplex},
		{"an alias inside its own anchor", "version: 2\nlayers: &l [a, *l]\n", errSyntax},
		{"a long scalar expanded by many aliases", aliasedScalar("", 128<<10, 43_000), errTooComplex},
		{"a binary value expanded by many aliases", aliasedScalar("!!binary ", 96<<10, 4000), errSyntax},
		{"long keys that differ only at the end, expanded by aliases", longKeys(1500, 500, 8), errTooComplex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err := Waivers([]byte(tt.in))
			diags := Diagnose([]byte(tt.in))
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, tt.want) {
				t.Errorf("Waivers error = %v, want %v", err, tt.want)
			}
			if len(diags) != 1 {
				t.Errorf("Diagnose = %+v, want one diagnostic", diags)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 256<<20 {
				t.Errorf("allocated %d MB", alloc>>20)
			}
			if elapsed > 5*time.Second {
				t.Errorf("took %v", elapsed)
			}
		})
	}

	legit := repeat(maxModules, func(i int) string { return fmt.Sprintf("  m%d: {}\n", i) })
	if mods, err := Modules([]byte(legit)); err != nil || len(mods) != maxModules {
		t.Errorf("Modules(%d modules) = %d, %v", maxModules, len(mods), err)
	}
}

// aliasedScalar is a policy with one anchored scalar of size bytes, of base64 text, used by n aliases.
func aliasedScalar(tag string, size, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: 2\nx: &a %s%s\ny: [", tag, strings.Repeat("QUJD", size/4))
	for range n {
		b.WriteString("*a,")
	}
	b.WriteString("]\n")
	return b.String()
}

// longKeys is an anchored mapping of n keys of size bytes that differ only in their last characters, used by
// uses aliases.
func longKeys(n, size, uses int) string {
	var b strings.Builder
	b.WriteString("version: 2\nx: &m\n")
	for i := range n {
		fmt.Fprintf(&b, "  %s%06d: 1\n", strings.Repeat("k", size-6), i)
	}
	b.WriteString("y: [")
	for range uses {
		b.WriteString("*m,")
	}
	b.WriteString("]\n")
	return b.String()
}

// A text area shows a policy with 5 000 modules in realistic stanzas; every reader must take it.
func TestReadersTakeAFullSizePolicy(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 2\nlayers: [core, edge]\nmodules:\n")
	for i := range maxModules {
		fmt.Fprintf(&b, "  mod%04d:\n    owner: team-%d\n    layer: core\n    deploy_unit: svc-%d\n    volatility: low\n"+
			"    subdomain: core\n    paths: [\"pkg/m%d/**\"]\n    public: [\"pkg/m%d/api\"]\n    internal: [\"pkg/m%d/internal/**\"]\n",
			i, i%40, i%90, i, i, i)
	}
	policy := []byte(b.String())
	if len(policy) > maxInputBytes {
		t.Fatalf("fixture is %d bytes, over the input limit", len(policy))
	}
	if mods, err := Modules(policy); err != nil || len(mods) != maxModules {
		t.Errorf("Modules = %d modules, %v", len(mods), err)
	}
	if err := Validate(policy); err != nil {
		t.Errorf("Validate = %v", err)
	}
	if got := Diagnose(policy); got != nil {
		t.Errorf("Diagnose = %+v", got[:min(3, len(got))])
	}
}

// NEL, LS and PS are text to the engine. Readers that report no line read them; the ones that do refuse them.
func TestLineBreaksOnlyMatterWhereLinesAre(t *testing.T) {
	for _, r := range []string{"\u0085", "\u2028", "\u2029"} {
		policy := []byte("version: 2\nmodules:\n  a: {}\nwaivers:\n  - rule: r\n    reason: \"pasted" + r + "text\"\n" +
			"    approved_by: me\n    expires: 2026-10-20\n")
		if w, err := Waivers(policy); err != nil || len(w) != 1 {
			t.Errorf("%q: Waivers = %v, %v; want the waiver", r, w, err)
		}
		if err := Validate(policy); err != nil {
			t.Errorf("%q: Validate = %v", r, err)
		}
		if _, err := Modules(policy); !errors.Is(err, errLineBreak) {
			t.Errorf("%q: Modules error = %v, want the line-break error", r, err)
		}
		if got := Diagnose(policy); len(got) != 1 || !strings.Contains(got[0].Message, "NEL, LS or PS") {
			t.Errorf("%q: Diagnose = %+v", r, got)
		}
	}
}

func TestExcessiveAliasingIsTooComplex(t *testing.T) {
	if err := newSyntaxError(errors.New("yaml: document contains excessive aliasing")); !errors.Is(err, errTooComplex) {
		t.Errorf("error = %v, want errTooComplex", err)
	}
}
