package ui

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
)

var pageNames = []string{"error"}

// page is what every template gets; Body is the page's own data.
type page struct {
	Title  string
	Login  string
	CSRF   string
	Repo   string // full name of the authorized repository, "" outside repository routes
	Notice string
	Error  string
	Body   any
}

// parsePages panics only on a broken embedded template, which every test of this package would catch.
func parsePages() map[string]*template.Template {
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		pages[name] = template.Must(template.ParseFS(templatesFS, "templates/layout.html", "templates/"+name+".html"))
	}
	return pages
}

func (s *server) newPage(r *http.Request, sc scope, title string, body any) page {
	return page{
		Title:  title,
		Login:  sc.user.Login,
		CSRF:   csrfToken(sc.user),
		Repo:   sc.repo.FullName,
		Notice: notices[r.URL.Query().Get("notice")],
		Body:   body,
	}
}

// render executes into a buffer first, so a template failure is a clean 500 and never half a page.
func (s *server) render(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, "layout", p); err != nil {
		logOf(r).outcome = "template"
		slog.Error("ui template failed", "page", name)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// fail answers with the error page and the documented status.
func (s *server) fail(w http.ResponseWriter, r *http.Request, sc scope, err error) {
	status, outcome, sentence := explain(err)
	logOf(r).outcome = outcome
	if outcome == "rate_limited" {
		w.Header().Set("Retry-After", retryAfterSeconds)
	}
	p := s.newPage(r, sc, http.StatusText(status), nil)
	p.Error = sentence
	s.render(w, r, status, "error", p)
}

func repoPath(sc scope, suffix string) string {
	return "/r/" + sc.repo.FullName + suffix
}
