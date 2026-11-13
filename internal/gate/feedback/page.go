package feedback

import "github.com/alexei-led/archfit-app/internal/wire/result"

// Page is the page form of the comment model. Text is untrusted; a consumer must
// render it as text or inert Markdown, never HTML.
type Page struct {
	Status     result.Status `json:"status"`
	StatusLine string        `json:"status_line"`
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
	Path     string          `json:"path,omitempty"`
	Line     int             `json:"line,omitempty"`
	Rule     string          `json:"rule"`
	What     string          `json:"what"`
	Why      string          `json:"why,omitempty"`
	Instead  string          `json:"instead,omitempty"`
	Balance  *result.Balance `json:"balance,omitempty"`
	Next     result.Next     `json:"next"`
	ShowNext bool            `json:"show_next"`
	Request  int             `json:"request,omitempty"`
}

// BuildPage renders the same model as publication. The caller supplies the
// App's judged result; this function grants no authority to an uploaded claim.
func BuildPage(r result.Result) Page {
	return newView(r).page()
}

func (v view) page() Page {
	p := Page{
		Status: v.result.Status, StatusLine: heading(v.result.Summary),
		Records: make([]Record, 0, len(v.result.Errors)+len(v.result.Unknown)),
		Author:  v.author, Owner: v.owner,
	}
	for _, e := range v.result.Errors {
		var balance *result.Balance
		if e.Balance != nil {
			balance = new(*e.Balance)
		}
		p.Records = append(p.Records, Record{
			Path: e.Path, Line: e.Line, Rule: e.Rule, What: e.What,
			Why: e.Why, Instead: e.Instead, Balance: balance,
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
