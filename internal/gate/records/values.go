package records

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Schema names. Every value carries its name in "schema"; a read refuses any other name.
const (
	SchemaRepo          = "archfit-app.repo.v1"
	SchemaRequest       = "archfit-app.request.v1"
	SchemaRequestIndex  = "archfit-app.request-index.v1"
	SchemaApproval      = "archfit-app.approval.v1"
	SchemaProposal      = "archfit-app.proposal.v1"
	SchemaProposalIndex = "archfit-app.proposal-index.v1"
	SchemaPull          = "archfit-app.pull.v1"
	SchemaPullIndex     = "archfit-app.pull-index.v1"
	SchemaRepos         = "archfit-app.repos.v1"
	SchemaPolicyPush    = "archfit-app.policy-push.v1"
)

// States of requests, approvals and proposals.
const (
	StateOpen     = "open"
	StateAnswered = "answered" // a request an owner answered
	StateClosed   = "closed"   // a request closed without an answer, e.g. one a deny rule already answers
	StateVoid     = "void"     // an approval or proposal that no longer applies
	StateMerged   = "merged"   // a proposal consumed by its merge commit
)

// Approval kinds (M2 plan, "Approval records").
const (
	KindApprove      = "approve"
	KindApproveUntil = "approve-until"
	KindReject       = "reject"
	KindFirst        = "first"
	KindWorkflow     = "workflow"
	KindConfirm      = "confirm"
	KindRevert       = "revert"
)

// MaxApprovals is the most approvals one proposal holds (M2 plan, "Approval records").
const MaxApprovals = 9

// maxValueBytes is the Store's value limit (store/module.md).
const maxValueBytes = 64 << 10

var (
	// ErrTooLarge is returned when an encoded value is over the Store's value limit.
	ErrTooLarge = errors.New("records: value over the 64 KiB limit")
	// ErrNoRecord is returned when a record that must exist does not.
	ErrNoRecord = errors.New("records: no record")
)

// Repo is the state of one repository at RepoKey.
type Repo struct {
	Schema string `json:"schema"`
	// ApprovedDigest is the digest of the last policy the owners approved.
	ApprovedDigest string `json:"approved_digest,omitempty"`
	// ApprovedCommit is the merge commit that brought ApprovedDigest; a consume approves only a commit after it.
	ApprovedCommit string `json:"approved_commit,omitempty"`
	// GuardedSince is the commit of the first approval: change control started there, so a push that happened before
	// it is not outside the flow, whenever it is delivered.
	GuardedSince string `json:"guarded_since,omitempty"`
	// OutsideFlow is set while the default branch has a policy that no approval made.
	OutsideFlow *OutsideFlow `json:"outside_flow,omitempty"`
	DriftIssue  int          `json:"drift_issue,omitempty"`
	// ExpiryIssues maps an exception key to the issue that warns of its date.
	ExpiryIssues map[string]int `json:"expiry_issues,omitempty"`
}

// OutsideFlow is "policy changed outside the flow": the digest seen and the issue opened for it (0 until it exists).
type OutsideFlow struct {
	Digest string `json:"digest"`
	Issue  int    `json:"issue,omitempty"`
}

// Request is one request at RequestKey.
type Request struct {
	Schema  string  `json:"schema"`
	Key     string  `json:"key"` // the request key as wire/request renders it
	Reason  string  `json:"reason"`
	Numbers Numbers `json:"numbers"`
	Waiting []int   `json:"waiting,omitempty"` // pull requests waiting on this request
	// Listed are the pull requests the issue body lists. An upload joins a pull request in the store alone, so the body
	// can lag Waiting until a request line renders it again.
	Listed []int  `json:"listed,omitempty"`
	State  string `json:"state"`
	// What the issue asks, as wire/request's Issue names it: the rule, the path of unowned code, the two modules
	// otherwise. With Expires and Record they let the App render the issue body again when a pull request joins.
	Rule    string `json:"rule,omitempty"`
	Path    string `json:"path,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Expires string `json:"expires,omitempty"` // the date the worker asked for, YYYY-MM-DD
	// Record is the record the issue shows: the asked errors as the check prints them, or the reported `can` answer.
	// It is check output, never source.
	Record string `json:"record,omitempty"`
}

// Numbers are a request's figures: verified from a stored result, or "as reported" by the CLI.
type Numbers struct {
	Verified bool     `json:"verified"`
	Balance  *Balance `json:"balance,omitempty"` // when the rule is about coupling
}

// Balance is "balance: score/max (min min)" of the record.
type Balance struct {
	Score int `json:"score"`
	Max   int `json:"max"`
	Min   int `json:"min"`
}

// Approval is one owner action at ApprovalKey.
type Approval struct {
	Schema  string `json:"schema"`
	Kind    string `json:"kind"`
	Request int    `json:"request,omitempty"` // the request issue, 0 when there is none
	// Change is what the approval does to the policy or binds to (head SHA, digests). The policy writer owns its shape.
	Change     json.RawMessage `json:"change"`
	Owner      int64           `json:"owner"`                // GitHub user id
	Login      string          `json:"login,omitempty"`      // the owner's login when they answered, for the pull request body
	Credential string          `json:"credential,omitempty"` // the passkey credential; empty for a reject
	At         time.Time       `json:"at"`
	// Proposal is set once, when the approval joins a proposal; the approval is consumed when that proposal is merged.
	Proposal string `json:"proposal,omitempty"`
	State    string `json:"state"` // open or void
}

// Proposal is the pull request that carries approvals to the default branch, at ProposalKey.
type Proposal struct {
	Schema string `json:"schema"`
	// Kind is "" for the App's policy pull request on archfit/policy, else ProposalFirst or ProposalWorkflow: a pull
	// request someone else wrote, which an owner approved as it is (M2 task 23).
	Kind           string   `json:"kind,omitempty"`
	Approvals      []string `json:"approvals"` // ordered approval ids, at most MaxApprovals
	PullRequest    int      `json:"pull_request,omitempty"`
	HeadSHA        string   `json:"head_sha,omitempty"`
	ExpectedDigest string   `json:"expected_digest"`
	// WorkflowDigest is the FileDigest of .github/workflows/archfit.yaml at HeadSHA that the approval admits; "" when it
	// admits none. Its merge must land that file too.
	WorkflowDigest string `json:"workflow_digest,omitempty"`
	State          string `json:"state"`
	Commit         string `json:"commit,omitempty"` // the consuming merge commit
}

// PolicyPush is the judgement of one default-branch push that changed the policy, at PolicyPushKey: the policy digest
// it brought and the proposal it consumed; Unguarded when the repository had no approved policy yet; neither when it
// was outside the flow. It is written once, in the swap that records the judgement, so a delivery of the same push
// again, even after later pushes, is judged the same way.
type PolicyPush struct {
	Schema    string `json:"schema"`
	Digest    string `json:"digest"`
	Proposal  string `json:"proposal,omitempty"`
	Unguarded bool   `json:"unguarded,omitempty"`
}

// Pull is the pull record of M2 D6 at PullKey: the latest verified upload of the pull request's head. No result
// bytes are stored; the result is read back from the run's artifact and checked against PayloadDigest.
type Pull struct {
	Schema        string   `json:"schema"`
	HeadSHA       string   `json:"head_sha"`
	RunID         int64    `json:"run_id"`
	RunAttempt    int      `json:"run_attempt"`
	PayloadDigest string   `json:"payload_digest"`
	Status        string   `json:"status"`
	Errors        []string `json:"errors,omitempty"` // the error keys of the result
	// RequestKeys holds, for each error of Errors in the same order, its request key (wire/request), "" when it has
	// none. The engine's keys are compared, never parsed, so the preview of an answer reads these to know which errors
	// the answer clears.
	RequestKeys []string `json:"request_keys,omitempty"`
}

// Value is a type of this package that Encode and Decode accept. stamp names the value's schema; for the record
// types it also sets Schema. check tests what JSON decoding cannot.
type Value interface {
	stamp() string
	check() error
}

func (v *Repo) stamp() string     { v.Schema = SchemaRepo; return v.Schema }
func (v *Request) stamp() string  { v.Schema = SchemaRequest; return v.Schema }
func (v *Approval) stamp() string { v.Schema = SchemaApproval; return v.Schema }
func (v *Proposal) stamp() string { v.Schema = SchemaProposal; return v.Schema }
func (v *Pull) stamp() string     { v.Schema = SchemaPull; return v.Schema }
func (v *PolicyPush) stamp() string {
	v.Schema = SchemaPolicyPush
	return v.Schema
}

func (*Repo) check() error       { return nil }
func (*Request) check() error    { return nil }
func (*Approval) check() error   { return nil }
func (*Pull) check() error       { return nil }
func (*PolicyPush) check() error { return nil }

func (v *Proposal) check() error {
	if len(v.Approvals) > MaxApprovals {
		return fmt.Errorf("records: a proposal of %d approvals, at most %d", len(v.Approvals), MaxApprovals)
	}
	return nil
}

// Encode returns the JSON of v with its schema set.
func Encode(v Value) ([]byte, error) {
	schema := v.stamp()
	if schema == "" {
		return nil, errors.New("records: encode: no schema")
	}
	if err := v.check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("records: encode %s: %w", schema, err)
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(raw) > maxValueBytes {
		return nil, ErrTooLarge
	}
	return raw, nil
}

// Decode reads raw into v. It refuses a value of another schema, an unknown field, and trailing data. Errors
// never quote the value.
func Decode(raw []byte, v Value) error {
	want := v.stamp()
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("records: unreadable %s value", want)
	}
	if head.Schema != want {
		return fmt.Errorf("records: the value is not a %s value", want)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("records: unreadable %s value", want)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("records: trailing data after the %s value", want)
	}
	return v.check()
}
