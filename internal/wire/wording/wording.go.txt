// Package testutil holds checks that tests of several packages share.
package testutil

import (
	"regexp"
	"strings"
)

// notUsedWords are the words of the "Not used" column of design section 5.
// Archfit output names each thing with one word; these are the other words.
// "record" is not listed: section 5 forbids it only as the verb for an
// owner's answer, and section 8 names the output a record. "review" is not
// listed: section 5 forbids it only as the name of the report, which
// cmd/archfit checks by name (TestReportIsNeverNamedReview).
var notUsedWords = []string{
	"config", "architecture file", "classification", "component",
	"approved pair", "edge", "relation", "law", "intent", "decision", "guardrail",
	"waiver", "baseline entry", "debt", "temporary decision", "finding", "violation",
	"blocker", "problem", "task", "gate", "scan", "blocked", "denied",
	"needs attention", "healthy", "decision request", "CR", "accept", "decline",
	"architect", "approver", "rating", "score", "analysis", "insight",
}

var storyOnlyWords = []string{
	"proposal", "void", "voided", "outside the flow", "YAML edit", "edit the YAML",
	"envelope", "digest", "hash", "sha", "reload", "refresh the page",
	"behind", "update branch", "admin merge", "squash", "rebase", "stale",
	"rung", "surface", "PR",
}

// gateNames are the two uses of "gate" that are names, not words: the check
// name and the policy key.
var gateNames = regexp.MustCompile(`(?i)archfit/gate|\bgate:`)

var notUsedPatterns = compileNotUsed(notUsedWords)
var storyOnlyPatterns = compileNotUsed(storyOnlyWords)

func compileNotUsed(words []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, len(words))
	for i, word := range words {
		phrase := strings.Join(strings.Fields(regexp.QuoteMeta(word)), `\s+`)
		patterns[i] = regexp.MustCompile(`(?i)(^|[^\pL\pN_])` + phrase + `($|[^\pL\pN_])`)
	}
	return patterns
}

// NotUsedIn returns the words of the Not used column that text uses, as
// whole words and in any case, in list order. A phrase matches across any
// whitespace.
func NotUsedIn(text string) []string {
	return notUsedIn(text, notUsedWords, notUsedPatterns)
}

// StoryNotUsedIn applies the extended story vocabulary.
func StoryNotUsedIn(text string) []string {
	return append(notUsedIn(text, notUsedWords, notUsedPatterns), notUsedIn(text, storyOnlyWords, storyOnlyPatterns)...)
}

func notUsedIn(text string, words []string, patterns []*regexp.Regexp) []string {
	text = gateNames.ReplaceAllString(text, " ")
	var found []string
	for i, pattern := range patterns {
		if pattern.MatchString(text) {
			found = append(found, words[i])
		}
	}
	return found
}
