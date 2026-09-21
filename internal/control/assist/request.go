package assist

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxInstructionBytes = 2000
	draftBranchPrefix   = "archfit/"
)

var (
	branchPattern  = regexp.MustCompile(`^archfit/[A-Za-z0-9._/-]+$`)
	headSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// validate reports ErrInvalidRequest with the name of the failed field, never its value.
func validate(mode Mode, r Request) error {
	var field string
	switch {
	case mode != ModeManaged && mode != ModeCustomer:
		field = "mode"
	case strings.TrimSpace(r.Instruction) == "" || len(r.Instruction) > maxInstructionBytes:
		field = "instruction"
	case !validBranch(r.Branch) || r.Branch == r.Repo.DefaultBranch:
		field = "branch"
	case !headSHAPattern.MatchString(r.HeadSHA):
		field = "head_sha"
	case r.By == "":
		field = "by"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidRequest, field)
}

// validBranch also rejects what the pattern lets through: empty, "." and ".." path segments.
func validBranch(branch string) bool {
	if !branchPattern.MatchString(branch) {
		return false
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(branch, draftBranchPrefix), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
