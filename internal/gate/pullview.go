package gate

import (
	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// PullView is the page form of the shared check and comment model.
type PullView = feedback.Page

// PullRecord is one error or unknown reason in a PullView.
type PullRecord = feedback.Record

// PullNext is the one step named by a PullRecord.
type PullNext = result.Next

// PullBalance is the coupling number shown by a PullRecord.
type PullBalance = result.Balance

// BuildPullView renders the App's judged result through the feedback model.
// It performs no evidence verification; callers must establish the result's
// authority before showing it as a verdict.
func BuildPullView(r result.Result) PullView {
	return feedback.BuildPage(r)
}
