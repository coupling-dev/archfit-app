package assist

import (
	"fmt"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
)

const maxInstructionBytes = 2000

// validate reports ErrInvalidRequest with the name of the failed field, never its value.
// The head SHA is not checked here: openDraft checks it once the draft is known to be open.
func validate(mode Mode, r Request) error {
	var field string
	switch {
	case mode != ModeManaged && mode != ModeCustomer:
		field = "mode"
	case strings.TrimSpace(r.Instruction) == "" || len(r.Instruction) > maxInstructionBytes:
		field = "instruction"
	case !proposal.ValidBranch(r.Branch) || r.Branch == r.Repo.DefaultBranch:
		field = "branch"
	case r.By == "":
		field = "by"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidRequest, field)
}
