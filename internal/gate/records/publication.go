package records

import (
	"strconv"
	"time"
)

// PublicationKey names the publication of a pull request at one head.
func PublicationKey(fullName string, pull int, head string) string {
	return "v4/publication/" + repo(fullName) + "/" + strconv.Itoa(pull) + "/" + head
}

// Publication is the result successfully published by the App. Pending means a write
// is in progress or could not be confirmed; Unpublished marks an upload deliberately not published,
// and Omitted means its body did not fit.
type Publication struct {
	Schema      string    `json:"schema"`
	HeadSHA     string    `json:"head_sha"`
	RunID       int64     `json:"run_id"`
	RunAttempt  int       `json:"run_attempt"`
	Result      []byte    `json:"result,omitempty"`
	Digest      string    `json:"digest,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	Source      string    `json:"source,omitempty"`
	Staged      bool      `json:"staged,omitempty"`
	Pending     bool      `json:"pending,omitempty"`
	Token       string    `json:"token,omitempty"`
	Superseded  bool      `json:"superseded,omitempty"`
	Unpublished bool      `json:"unpublished,omitempty"`
	Omitted     bool      `json:"omitted,omitempty"`
}

func (v *Publication) stamp() string { v.Schema = "archfit-app.publication.v1"; return v.Schema }
func (*Publication) check() error    { return nil }
