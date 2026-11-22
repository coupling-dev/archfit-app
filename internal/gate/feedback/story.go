package feedback

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/wire/result"
)

// The steps of the story of a change (task 8.18, section 1) and the values a step takes (contract archfit-story v1).
const (
	stepDone      = "done"
	stepCurrent   = "current"
	stepFailed    = "failed"
	stepLater     = "later"
	stepNotNeeded = "not-needed"
)

// StoryFacts is what the App knows of the request a pull request waits on, read when its check is published.
type StoryFacts struct {
	State        string // records.StateOpen, StateAnswered or StateClosed; "" when unknown
	PolicyPull   int    // the App's policy pull request that carries the answer; 0 when none
	PolicyMerged bool   // that pull request merged
	Owner        string // the GitHub login of the person who answers; "" when the policy names no person
}

// StoryReader reads the facts of request number of r. A nil reader leaves the story to the result alone.
type StoryReader func(ctx context.Context, r api.Repo, request int) (StoryFacts, error)

// storyFacts reads the facts of v's first request. The story only informs: a failed read leaves the facts out.
func (s *publisher) storyFacts(ctx context.Context, p Publication4, v view) *StoryFacts {
	if s.story == nil || len(v.requests) == 0 {
		return nil
	}
	facts, err := s.story(ctx, p.Target.Repo, v.requests[0])
	if err != nil {
		slog.Default().WarnContext(ctx, "feedback: read the story facts", "repository", p.Target.Repo.FullName,
			"request", v.requests[0], "error", err.Error())
		return nil
	}
	return &facts
}

// storySteps keeps the contract's step order in the JSON.
type storySteps struct {
	Change  string `json:"change"`
	Check   string `json:"check"`
	Ask     string `json:"ask"`
	Answer  string `json:"answer"`
	Policy  string `json:"policy"`
	Recheck string `json:"recheck"`
}

type story struct {
	Steps      storySteps `json:"steps"`
	Request    int        `json:"request,omitempty"`
	WaitingFor string     `json:"waiting_for,omitempty"`
	Next       string     `json:"next"`
}

// storyPrefix and storySuffix frame the one hidden line `archfit status` reads from the check output text.
const (
	storyPrefix = "<!-- archfit-story v1 "
	storySuffix = " -->"
	// maxStoryLine bounds the whole line, after JSON escaping; only the next text is shortened to fit it.
	maxStoryLine = 1023
)

// storyLine is the archfit-story v1 line of v, with facts of its first request when there are any. json.Marshal
// escapes <, > and &, so no text in it can close the comment.
func storyLine(v view, facts *StoryFacts) string {
	s := storyOf(v, facts)
	line := encodeStory(s)
	// Escaping can grow a byte to six (\u003c), so the bound is measured on the encoded line. Each pass drops the
	// next text's last rune, which keeps it valid UTF-8; the fixed parts alone are far below the bound.
	for len(line) > maxStoryLine && s.Next != "" {
		over := len(line) - maxStoryLine
		next := strings.TrimSuffix(s.Next, "…")
		if next == "" {
			s.Next = ""
			line = encodeStory(s)
			break
		}
		end := max(len(next)-max(over/6, 1), 0)
		for end > 0 && !utf8.RuneStart(next[end]) {
			end--
		}
		s.Next = next[:end] + "…"
		line = encodeStory(s)
	}
	return line
}

func encodeStory(s story) string {
	raw, _ := json.Marshal(s) // strings and ints always encode
	return storyPrefix + string(raw) + storySuffix
}

func storyOf(v view, facts *StoryFacts) story {
	s := story{Steps: storySteps{Change: stepDone, Check: stepDone, Ask: stepNotNeeded, Answer: stepNotNeeded,
		Policy: stepNotNeeded, Recheck: stepNotNeeded}}
	switch v.result.Status {
	case result.StatusPass:
		s.Next = "nothing to do: the check passes."
		return s
	case result.StatusUnknown:
		s.Steps.Check = stepCurrent
		s.Next = "wait: the check could not decide yet."
		if len(v.result.Unknown) > 0 && v.result.Unknown[0].Next.Text != "" {
			s.Next = v.result.Unknown[0].Next.Text
		}
		return s
	}
	s.Steps.Check, s.Next = stepFailed, v.author
	switch v.covered {
	case nextFix:
		return s
	case nextCommand:
		s.Steps.Ask, s.Steps.Answer, s.Steps.Policy, s.Steps.Recheck = stepCurrent, stepLater, stepLater, stepLater
		return s
	case nextWait:
	default:
		s.Steps.Ask, s.Steps.Answer, s.Steps.Policy, s.Steps.Recheck = stepCurrent, stepLater, stepLater, stepLater
		return s
	}
	s.Request = v.requests[0]
	n := "#" + strconv.Itoa(s.Request)
	switch {
	case facts != nil && facts.State == records.StateAnswered && facts.PolicyPull > 0 && !facts.PolicyMerged:
		s.Steps.Ask, s.Steps.Answer, s.Steps.Policy, s.Steps.Recheck = stepDone, stepDone, stepCurrent, stepLater
		s.Next = "nothing to do. " + n + " is answered; the App is merging policy pull request #" +
			strconv.Itoa(facts.PolicyPull) + ", then this check runs again by itself."
	case facts != nil && facts.State == records.StateAnswered:
		s.Steps.Ask, s.Steps.Answer, s.Steps.Policy, s.Steps.Recheck = stepDone, stepDone, stepDone, stepCurrent
		s.Next = "nothing to do. " + n + " is answered and the policy has it; this check runs again by itself."
	default:
		// The request is open: the owner answers it.
		s.Steps.Ask, s.Steps.Answer, s.Steps.Policy, s.Steps.Recheck = stepCurrent, stepLater, stepLater, stepLater
		if facts != nil {
			s.WaitingFor = facts.Owner
		}
	}
	return s
}
