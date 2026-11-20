package feedback

import "github.com/coupling-dev/archfit-app/internal/wire/result"

// Page is the page form of the comment model. Text is untrusted; a consumer must
// render it as text or inert Markdown, never HTML.
type Page struct {
	Status     result.Status `json:"status"`
	StatusLine string        `json:"status_line"`
	Note       string        `json:"note,omitempty"`
	Records    []Record      `json:"records"`
	Author     string        `json:"author,omitempty"`
	Owner      string        `json:"owner,omitempty"`
}

// Record contains the fields shown by the check and comment, in their order.
// Rule is the rule id for an error, or the cause for an unknown reason.
// ShowNext is false when the author line already names Next.
// Request is the issue number named by this record's wait step, even on a fail
// caused by an expired exception.
type Record struct {
	Path    string          `json:"path,omitempty"`
	Line    int             `json:"line,omitempty"`
	Rule    string          `json:"rule"`
	What    string          `json:"what"`
	Why     string          `json:"why,omitempty"`
	Instead string          `json:"instead,omitempty"`
	Balance *result.Balance `json:"balance,omitempty"`
	// Subject is the module a balance's inputs name, the error's target; set only when the balance has inputs.
	Subject  string      `json:"subject,omitempty"`
	Next     result.Next `json:"next"`
	ShowNext bool        `json:"show_next"`
	Request  int         `json:"request,omitempty"`
}

// BuildPage renders the same model as publication. The caller supplies the
// App's judged result; this function grants no authority to an uploaded claim.
func BuildPage(r result.Result) Page {
	return newView(r).page()
}

func (v view) page() Page {
	p := Page{
		Status: v.result.Status, StatusLine: heading(v.result.Summary), Note: unknownNote(v.result),
		Records: make([]Record, 0, len(v.result.Errors)+len(v.result.Unknown)),
		Author:  v.author, Owner: v.owner,
	}
	for _, e := range v.result.Errors {
		var balance *result.Balance
		var subject string
		if e.Balance != nil {
			balance = new(*e.Balance)
			if e.Balance.HasInputs() {
				subject = e.To
			}
		}
		p.Records = append(p.Records, Record{
			Path: e.Path, Line: e.Line, Rule: e.Rule, What: e.What,
			Why: e.Why, Instead: e.Instead, Balance: balance, Subject: subject,
			Next: e.Next, ShowNext: !v.says(e.Next), Request: request(e.Next),
		})
	}
	for _, u := range v.result.Unknown {
		p.Records = append(p.Records, Record{
			Path: u.Path, Line: u.Line, Rule: u.Cause, What: u.What,
			Why: u.Why, Next: u.Next, ShowNext: !v.says(u.Next),
		})
	}
	return p
}

// unknownNote is the one plain sentence under an unknown heading. It is true for every unknown reason: the reasons
// share causes, so a sentence about a cause would state a fact that some of them contradict.
func unknownNote(r result.Result) string {
	if r.Status != result.StatusUnknown {
		return ""
	}
	return unknownSentence
}

// unknownSentence says why there is no verdict; the record below says what to do.
const unknownSentence = "Archfit could not check this pull request. The record below says what to do."
