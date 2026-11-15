package feedback

import (
	"strings"
)

// maxBody is the GitHub limit for a check summary; the comment uses the same budget.
const maxBody = 65535

// Engine-authored strings are untrusted for Markdown. Removing the marker parts and backticks first and
// escaping the angle brackets after makes any reassembled "<!--" inert.
var (
	stripper = strings.NewReplacer("<!--", "", "-->", "", "`", "", "\r", "", "\n", "")
	escaper  = strings.NewReplacer("<", "&lt;", ">", "&gt;")
)

func sanitize(s string) string {
	return escaper.Replace(stripper.Replace(s))
}
