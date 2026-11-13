package gate

import (
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
)

// ScreenInput, ScreenReason and ScreenLine are owned by gate/feedback: the pull request page words a published
// result with the code that words the comment.
type (
	ScreenInput  = feedback.ScreenInput
	ScreenReason = feedback.ScreenReason
	ScreenLine   = feedback.Line
)

// FirstScreen is the comment's first screen for a published result, as lines of plain text; nil when it would show
// no report.
func FirstScreen(in ScreenInput) []ScreenLine { return feedback.FirstScreen(in) }

// RecoveryStep is the next step of a recorded reason, worded for its role as the comment words it.
func RecoveryStep(code, next string) string { return feedback.RecoveryStep(code, next) }
