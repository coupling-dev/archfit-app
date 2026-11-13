// Package sweep repairs gate state that webhooks and uploads missed.
package sweep

// Summary holds counts only; it is safe to log.
type Summary struct {
	Repos, Pulls                    int
	Pended, TimedOut, Rerun, Warned int
	Suspended                       int
	NotAdmitted                     int  // installations skipped: no operator approved them
	Errors                          int  // repositories or pulls skipped after an error; details are in the log
	RateLimited                     bool // the pass stopped early on a GitHub rate limit
}
