package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCodeSpan(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pkg_tc", "`pkg_tc`"},
		{"a`b", "``a`b``"},
		{"`x", "`` `x ``"},
		{"line\nbreak", "`line break`"},
		{"@owner [x](http://e)", "`@owner [x](http://e)`"},
	} {
		if got := CodeSpan(tc.in); got != tc.want {
			t.Errorf("CodeSpan(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFencedCannotBeClosedByItsContent(t *testing.T) {
	got := Fenced("a\n```\n# heading @owner\n")
	if !strings.HasPrefix(got, "````text\n") || !strings.HasSuffix(got, "\n````\n") {
		t.Errorf("Fenced = %q", got)
	}
	if got := Fenced("plain"); got != "```text\nplain\n```\n" {
		t.Errorf("Fenced(plain) = %q", got)
	}
}

func TestClip(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5} {
		got := Clip("éééé", n)
		if len(got) > n || !utf8.ValidString(got) {
			t.Errorf("Clip to %d bytes = %q", n, got)
		}
	}
	if got := Clip("short", 10); got != "short" {
		t.Errorf("Clip kept %q", got)
	}
	got := Clip(strings.Repeat("é", 10), 9)
	if len(got) > 9 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("Clip = %q (%d bytes)", got, len(got))
	}
}
