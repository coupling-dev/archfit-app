package policy3

import (
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"
)

var requestCommentRE = regexp.MustCompile(`(?:^|[^[:alnum:]_])#([1-9][0-9]*)(?:$|[^[:alnum:]_])`)

// ExceptionRequests returns request numbers from first-line comments on exception entries.
// An entry without a request comment has number zero. Call Decode before using the result.
func ExceptionRequests(raw []byte) []int {
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "exceptions" {
			continue
		}
		seq := root.Content[i+1]
		out := make([]int, len(seq.Content))
		for j, item := range seq.Content {
			var comment string
			for _, node := range item.Content {
				if node.Line == item.Line && node.LineComment != "" {
					comment = node.LineComment
					break
				}
			}
			if item.LineComment != "" {
				comment = item.LineComment
			}
			m := requestCommentRE.FindStringSubmatch(comment)
			if len(m) > 1 {
				out[j], _ = strconv.Atoi(m[1])
			}
		}
		return out
	}
	return nil
}
