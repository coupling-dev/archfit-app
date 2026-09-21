package policy

import (
	"fmt"
	"strings"
	"testing"
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
