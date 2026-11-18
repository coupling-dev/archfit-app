package ui

import (
	"errors"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestOwnerViewReadsPasskeysBesideTheView(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.owners.view = ownerFixture(gate.OwnerFirst, 0, false)
		e.owners.onView, e.passkeys.onList = meet(t)
		wantStatus(t, e.as("alice", permRead).get(ownerAPI+"first/view"), http.StatusOK)
	})
}

// The view's refusal is the answer, whatever the inventory read did.
func TestOwnerViewRefusalWinsOverAnUnreadableInventory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.owners.err = gate.ErrOwnerFileUnreadable
		e.passkeys.err = errors.New("store down")
		wantStatus(t, e.as("alice", permRead).get(ownerAPI+"revert/view"), http.StatusConflict)
	})
}

func TestPolicyReadsStateBesideTheFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.gh.setFile(repoName, mainBranch, ".archfit.yaml", policyText)
		e.policy.state.ApprovedDigest = policy3.Digest([]byte(policyText))
		e.gh.onFile, e.policy.onState = meet(t)
		wantStatus(t, e.as("alice", permRead).get(policyAPI), http.StatusOK)
	})
}

// The file's answer keeps priority over the state's error, as when the state was read after the file.
func TestPolicyFileErrorsWinOverAnUnreadableState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     string
		wantCode int
	}{
		{name: "no policy file", wantCode: http.StatusConflict},
		{name: "unreadable policy file", file: "version: 2\n", wantCode: http.StatusConflict},
		{name: "readable policy file", file: policyText, wantCode: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newEnv(t)
				e.policy.err = errors.New("store down")
				if tc.file != "" {
					e.gh.setFile(repoName, mainBranch, ".archfit.yaml", tc.file)
				} else {
					delete(e.gh.files, repoName+"@"+mainBranch+":.archfit.yaml")
				}
				res := e.as("alice", permRead).get(policyAPI)
				wantStatus(t, res, tc.wantCode)
			})
		})
	}
}
