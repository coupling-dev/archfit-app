package report

import (
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Caps. Every number is [default]: decisions.md fixes only MaxBytes.
const (
	maxFindings         = 10_000
	maxAgentTasks       = 10_000
	maxSeams            = 50_000
	maxUnevaluatedRules = 1_000
	maxLocations        = 100
	maxAlternatives     = 32
	maxTaskStrings      = 32 // constraints, validation
	maxTaskFiles        = 1_000
	maxDimensionFacts   = 256 // metrics, unknown, delta metrics
	maxFindingRefs      = 10_000
	maxSmallList        = 64 // producers, unknowns, coverage.tools, every reasons list

	maxTextLen       = 500
	maxCommandLen    = 512
	maxIdentifierLen = 200
	maxTokenLen      = 64
	maxVersionLen    = 128
	maxPathLen       = 512
	configHashLen    = 64
	maxLine          = 10_000_000
)

// phase orders the checks: the error of the earliest failed phase is the one Decode returns.
type phase int

const (
	phaseRequired phase = iota
	phaseVersion
	phaseEnum
	phaseCap
	phaseString
	phasePath
	phaseDuplicate
	phaseCount
)

// strClass is the rule set of a kept string.
type strClass int

const (
	classText strClass = iota
	classCommand
	classIdentifier
	classToken
	classVersion // also the model, labels and settings hashes
	classConfigHash
	classPath
	classEndpointPath // a path, or empty
)

// checker validates the shadow report while it copies the kept fields. It keeps the first error of each phase.
type checker struct {
	path []seg
	errs [phaseCount]error
	ids  map[string]struct{} // finding IDs seen
}

func (c *checker) err() error {
	for _, err := range c.errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// fail records a broken rule at key under the current path; key "" is the current path itself.
func (c *checker) fail(p phase, key, rule string) {
	if c.errs[p] == nil {
		c.errs[p] = locError(formatLoc(append(slices.Clone(c.path), seg{key, -1})), rule)
	}
}

// str checks a required string; opt checks one whose absence decodes to "".
func (c *checker) str(key string, s *string, class strClass) string {
	if s == nil {
		c.fail(phaseRequired, key, "is required")
		return ""
	}
	return c.opt(key, *s, class)
}

func (c *checker) opt(key, s string, class strClass) string {
	if p, rule := stringRule(s, class); rule != "" {
		c.fail(p, key, rule)
	}
	return s
}

func (c *checker) enum(key string, s *string, allowed ...string) string {
	if s == nil {
		c.fail(phaseRequired, key, "is required")
		return ""
	}
	if !slices.Contains(allowed, *s) {
		c.fail(phaseEnum, key, "must be one of "+strings.Join(allowed, ", "))
	}
	return *s
}

// count checks a required integer in 0..limit.
func (c *checker) count(key string, n *int, limit int) int {
	if n == nil {
		c.fail(phaseRequired, key, "is required")
		return 0
	}
	if *n < 0 || *n > limit {
		c.fail(phaseString, key, "is out of range")
	}
	return *n
}

func (c *checker) number(key string, f *float64) float64 {
	if f == nil {
		c.fail(phaseRequired, key, "is required")
		return 0
	}
	return *f
}

// object converts a required object.
func object[S, T any](c *checker, key string, in *S, conv func(*S) T) (out T) {
	if in == nil {
		c.fail(phaseRequired, key, "is required")
		return out
	}
	return *optional(c, key, in, conv)
}

// optional converts an object whose absence decodes to nil.
func optional[S, T any](c *checker, key string, in *S, conv func(*S) T) *T {
	if in == nil {
		return nil
	}
	c.path = append(c.path, seg{key, -1})
	out := conv(in)
	c.path = c.path[:len(c.path)-1]
	return &out
}

// list converts a list of at most limit elements, in order. A required list is never nil; an optional one
// (omitempty) is nil when empty, so that a marshalled Report decodes to an equal Report.
func list[S, T any](c *checker, key string, in []S, limit int, required bool, conv func(*S) T) []T {
	if in == nil && required {
		c.fail(phaseRequired, key, "is required")
	}
	if len(in) > limit {
		c.fail(phaseCap, key, "has too many elements")
		return nil
	}
	if len(in) == 0 && !required {
		return nil
	}
	out := make([]T, 0, len(in))
	c.path = append(c.path, seg{key, -1})
	last := len(c.path) - 1
	for i := range in {
		c.path[last].index = i
		out = append(out, conv(&in[i]))
	}
	c.path = c.path[:last]
	return out
}

func stringsOf(c *checker, key string, in []string, limit int, required bool, class strClass) []string {
	return list(c, key, in, limit, required, func(s *string) string { return c.opt("", *s, class) })
}

// stringRule returns the phase and the rule that s breaks, or an empty rule.
func stringRule(s string, class strClass) (phase, string) {
	if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == ' ' || r == ' ' }) {
		return phaseString, "must be a single line without control characters"
	}
	switch class {
	case classText:
		return phaseString, tooLong(s, maxTextLen)
	case classIdentifier:
		return phaseString, tooLong(s, maxIdentifierLen)
	case classToken:
		return phaseString, tokenRule(s, maxTokenLen)
	case classVersion:
		return phaseString, tokenRule(s, maxVersionLen)
	case classCommand:
		if slices.ContainsFunc(strings.Fields(s), isRefreshFlag) {
			return phaseString, "must not contain --refresh"
		}
		return phaseString, tooLong(s, maxCommandLen)
	case classConfigHash:
		if s != "" && (len(s) != configHashLen || strings.ContainsFunc(s, notLowerHex)) {
			return phaseString, "must be empty or 64 lowercase hex characters"
		}
	case classPath:
		return phasePath, pathRule(s)
	case classEndpointPath:
		if s != "" {
			return phasePath, pathRule(s)
		}
	}
	return phaseString, ""
}

func tooLong(s string, limit int) string {
	if utf8.RuneCountInString(s) > limit {
		return "is too long"
	}
	return ""
}

func tokenRule(s string, limit int) string {
	if strings.ContainsFunc(s, notTokenChar) {
		return "may hold only letters, digits, space and _ . : / + -"
	}
	return tooLong(s, limit)
}

func notTokenChar(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return false
	}
	return !strings.ContainsRune(" _.:/+-", r)
}

func notLowerHex(r rune) bool {
	return (r < '0' || r > '9') && (r < 'a' || r > 'f')
}

func isRefreshFlag(word string) bool {
	return word == "--refresh" || strings.HasPrefix(word, "--refresh=")
}

// pathRule accepts a clean, relative, forward-slash path; a directory or a glob is a path.
func pathRule(p string) string {
	switch {
	case p == "":
		return "must not be empty"
	case utf8.RuneCountInString(p) > maxPathLen:
		return "is too long"
	case strings.Contains(p, `\`):
		return "must use forward slashes"
	case strings.HasPrefix(p, "/"), len(p) >= 2 && p[1] == ':' && unicode.IsLetter(rune(p[0])):
		return "must be relative"
	case p == ".", p == "..", strings.HasPrefix(p, "../"), path.Clean(p) != p:
		return "must be a clean path inside the repository"
	}
	return ""
}

func dimensionNames() [9]string {
	return [9]string{"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift"}
}

func isDimension(name string) bool {
	names := dimensionNames()
	return slices.Contains(names[:], name)
}
