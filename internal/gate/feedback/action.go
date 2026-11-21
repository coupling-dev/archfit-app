package feedback

import (
	"net/url"
	"strings"
)

// OwnerPage is the App page where a person resolves the one step a check waits for. The zero value means no such
// page: the check then shows only what the result says.
type OwnerPage struct {
	Label string // the action's name, as the page is titled: "Review the workflow change"
	Path  string // after /r/<owner>/<repo>: "/owner/workflow?pull=9"
	Role  string // who may act: "owner" or "administrator"
}

// OwnerLink is the App page of path for the repository fullName; "" without an App URL.
func OwnerLink(appURL, fullName, path string) string {
	if appURL == "" {
		return ""
	}
	owner, name, _ := strings.Cut(fullName, "/")
	return appURL + "/r/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + path
}

// nextLink is a pending owner step with its absolute URL, as the check and the comment show it.
type nextLink struct {
	label, url, role string
}

// withNext is v with the pending step page, linked for head.
func (v view) withNext(page OwnerPage, appURL, fullName, head string) view {
	if page.Path == "" {
		return v
	}
	if u := OwnerLink(appURL, fullName, page.Path); u != "" {
		v.next = &nextLink{label: page.Label, url: u, role: page.Role}
		v.head = head
	}
	return v
}

// nextLine is the one prominent line: the action, its link, who may act and that it asks for a passkey.
func (v view) nextLine() string {
	return NextLine(v.next.label, v.next.url, v.next.role)
}

// NextLine is the line every GitHub text of the App starts with when a person must act: the action as a link to its
// App page, who may act, and that the page asks for a passkey.
func NextLine(label, url, role string) string {
	return "**Next:** [" + label + "](" + url + ") (" + role + ", passkey)\n"
}

// mark is the status marker of the status line. An unknown that waits for a person shows as waiting.
func (v view) mark() string {
	switch {
	case v.result.Status == "pass":
		return "✅"
	case v.result.Status == "fail":
		return "❌"
	case v.result.Status == "waiting", v.next != nil:
		return "⏳"
	}
	return "⚠️"
}

// shortHead is the first seven characters of a commit.
func shortHead(sha string) string {
	return sha[:min(len(sha), 7)]
}

// lead starts the check summary when a person must act: the next line and what it covers. GitHub shows the title above
// the summary, so the status is not repeated. Without a pending step the summary is only the text block.
func (v view) lead() string {
	if v.next == nil {
		return ""
	}
	return v.nextLine() + "\n" + v.facts() + "\n"
}

// facts is the small table under the next line: who can act and the head.
func (v view) facts() string {
	var b strings.Builder
	b.WriteString("| | |\n| --- | --- |\n")
	policy := "approved"
	if v.next.role == "administrator" { // the first policy is not approved yet: the proposed one names who may
		policy = "proposed"
	}
	b.WriteString("| Who can act | an " + v.next.role + " named in the " + policy + " policy |\n")
	if v.head != "" {
		b.WriteString("| Head | `" + shortHead(v.head) + "` |\n")
	}
	return b.String()
}
