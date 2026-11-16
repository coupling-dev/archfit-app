package requests

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestDiffExactChanges(t *testing.T) {
	const headers = "--- a/p\n+++ b/p\n"
	tests := []struct {
		name, old, edited, want string
	}{
		{name: "empty files"},
		{name: "identical unterminated files", old: "a", edited: "a"},
		{name: "add file", edited: "a\n", want: headers + "@@ -0,0 +1,1 @@\n+a\n"},
		{name: "delete file", old: "a\n", want: headers + "@@ -1,1 +0,0 @@\n-a\n"},
		{name: "add final newline", old: "a", edited: "a\n",
			want: headers + "@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+a\n"},
		{name: "remove final newline", old: "a\n", edited: "a",
			want: headers + "@@ -1,1 +1,1 @@\n-a\n+a\n\\ No newline at end of file\n"},
		{name: "CRLF to LF", old: "a\r\n", edited: "a\n",
			want: headers + "@@ -1,1 +1,1 @@\n-a\n\\ Line ending: CRLF\n+a\n\\ Line ending: LF\n"},
		{name: "LF to CRLF", old: "a\n", edited: "a\r\n",
			want: headers + "@@ -1,1 +1,1 @@\n-a\n\\ Line ending: LF\n+a\n\\ Line ending: CRLF\n"},
		{name: "blank line", edited: "\n", want: headers + "@@ -0,0 +1,1 @@\n+\n"},
		{name: "two separated edits",
			old:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n",
			edited: "one\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\nthirteen\n",
			want: headers + "@@ -1,4 +1,4 @@\n-1\n+one\n 2\n 3\n 4\n" +
				"@@ -10,4 +10,4 @@\n 10\n 11\n 12\n-13\n+thirteen\n"},
		{name: "nearby edits share context", old: "a\nb\nc\nd\ne\n", edited: "A\nb\nc\nd\nE\n",
			want: headers + "@@ -1,5 +1,5 @@\n-a\n+A\n b\n c\n d\n-e\n+E\n"},
		{name: "insert and delete shift ranges",
			old:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n",
			edited: "new\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n",
			want: headers + "@@ -1,3 +1,4 @@\n+new\n 1\n 2\n 3\n" +
				"@@ -9,4 +10,3 @@\n 9\n 10\n 11\n-12\n"},
		{name: "mixed endings", old: "a\nb\r\nc\n", edited: "a\nb\nc\n",
			want: headers + "@@ -1,3 +1,3 @@\n a\n-b\n\\ Line ending: CRLF\n+b\n\\ Line ending: LF\n c\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff("p", []byte(tt.old), []byte(tt.edited)); got != tt.want {
				t.Errorf("diff = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiffVisibleText(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"HTML stays text", "<script>alert(\"x\")</script>", "<script>alert(\"x\")</script>"},
		{"ordinary Unicode", "café 日本語", "café 日本語"},
		{"tab", "\tx", "\\tx"},
		{"literal backslash", "\\tx", "\\\\tx"},
		{"literal backslash and quote", "\\\"", "\\\\\""},
		{"terminal escape", "\x1b[31m", "\\x1b[31m"},
		{"bare carriage return", "a\rb", "a\\rb"},
		{"bidi control", "a\u202eb", "a\\u202eb"},
		{"non-breaking space", "a\u00a0b", "a\\u00a0b"},
		{"invalid UTF-8", "\xff", "\\xff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := "--- a/p\n+++ b/p\n@@ -0,0 +1,1 @@\n+" + tt.want + "\n"
			if got := Diff("p", nil, []byte(tt.text+"\n")); got != want {
				t.Errorf("diff = %q, want %q", got, want)
			}
		})
	}
}

func FuzzDiffHunks(f *testing.F) {
	f.Add([]byte{1, 2, 1, 3}, []byte{2, 1, 4, 3})
	f.Add([]byte{}, []byte{1})
	f.Add([]byte{1}, []byte{})
	f.Add([]byte{1, 2}, []byte{1, 2})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Fuzz(func(t *testing.T, old, edited []byte) {
		if len(old) > 128 || len(edited) > 128 {
			t.Skip()
		}
		asLines := func(data []byte) []string {
			out := make([]string, len(data))
			for i, n := range data {
				out[i] = strconv.Itoa(int(n)) + "\n"
			}
			return out
		}
		a, b := asLines(old), asLines(edited)
		before, after := strings.Join(a, ""), strings.Join(b, "")
		diff := Diff("p", []byte(before), []byte(after))
		if before == after {
			if diff != "" {
				t.Fatalf("equal bytes have a diff: %q", diff)
			}
			return
		}
		rows := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
		if len(rows) < 4 || rows[0] != "--- a/p" || rows[1] != "+++ b/p" {
			t.Fatalf("missing change: %q", diff)
		}
		var got strings.Builder
		pos, oldLeft, newLeft, changes := 0, 0, 0, 0
		for _, row := range rows[2:] {
			if strings.HasPrefix(row, "@@") {
				if oldLeft != 0 || newLeft != 0 {
					t.Fatalf("wrong hunk counts: %q", diff)
				}
				var oldStart, newStart int
				if n, err := fmt.Sscanf(row, "@@ -%d,%d +%d,%d @@", &oldStart, &oldLeft, &newStart, &newLeft); err != nil || n != 4 {
					t.Fatalf("bad hunk header %q: %v", row, err)
				}
				if oldLeft != 0 {
					oldStart--
				}
				if newLeft != 0 {
					newStart--
				}
				if oldStart < pos || oldStart > len(a) {
					t.Fatalf("invalid old range: %q", diff)
				}
				got.WriteString(strings.Join(a[pos:oldStart], ""))
				if strings.Count(got.String(), "\n") != newStart {
					t.Fatalf("invalid new range: %q", diff)
				}
				pos = oldStart
				continue
			}
			if len(row) == 0 || !strings.ContainsRune(" +-", rune(row[0])) {
				t.Fatalf("bad line: %q", row)
			}
			line := row[1:] + "\n"
			if row[0] != '+' {
				if pos >= len(a) || a[pos] != line {
					t.Fatalf("old line mismatch: %q", diff)
				}
				pos++
				oldLeft--
			}
			if row[0] != '-' {
				got.WriteString(line)
				newLeft--
			}
			if row[0] != ' ' {
				changes++
			}
		}
		got.WriteString(strings.Join(a[pos:], ""))
		if got.String() != after || oldLeft != 0 || newLeft != 0 || changes == 0 {
			t.Fatalf("diff does not reconstruct edited bytes: %q", diff)
		}
	})
}

func TestDiffLargeReplacement(t *testing.T) {
	old, edited := strings.Repeat("old\n", 2000), strings.Repeat("new\n", 2000)
	want := "--- a/p\n+++ b/p\n@@ -1,2000 +1,2000 @@\n" +
		strings.Repeat("-old\n", 2000) + strings.Repeat("+new\n", 2000)
	if got := Diff("p", []byte(old), []byte(edited)); got != want {
		t.Fatalf("large replacement lost changed lines: got %d bytes, want %d", len(got), len(want))
	}
}
