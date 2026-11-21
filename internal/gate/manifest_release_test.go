package gate

import (
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	"github.com/coupling-dev/archfit-app/internal/wire/manifest"
)

// The release the shipped manifest approves: an image of each platform and the Action commit that runs it. Engine v4.0.0
// and its Action commit are no longer approved.
const (
	release4Image   = "sha256:be1eac8f73513c6d0ef4ff17a78255629e4e4bc2d1b3e044b49afcbfdd6aac29" // v4.0.0, linux/amd64
	release4Action  = "9669571b7b25e3f37fd7f1d99d7b89741c6c95d6"
	release09Image  = "sha256:e841f7b519e5d4a787407887f784f121dff9801c25d0bc892695d783d7805db4" // v0.9.0, linux/amd64
	release09Action = "53e0c49d80627efa4262d7ee78c56dd6054a6e43"
)

// TestUpload4ApprovesTheShippedRelease runs uploads against the embedded manifest, not the test approvals of world4:
// engine 0.9.0 by its Action is accepted; engine v4.0.0, its Action commit and the image index are not.
func TestUpload4ApprovesTheShippedRelease(t *testing.T) {
	for _, tc := range []struct {
		name          string
		engine, image string
		action        string
		status        string
		reason        string
	}{
		{name: "engine 0.9.0 by its Action", engine: "v0.9.0", image: release09Image, action: release09Action, status: "pass"},
		{name: "engine v4.0.0 by its old Action", engine: "v4.0.0", image: release4Image, action: release4Action,
			status: "unknown", reason: reasonUnknownEngine},
		{name: "engine v4.0.0 by the 0.9.0 Action", engine: "v4.0.0", image: release4Image, action: release09Action,
			status: "unknown", reason: reasonUnknownEngine},
		{name: "engine 0.9.0 claimed with the v4.0.0 image", engine: "v0.9.0", image: release4Image, action: release09Action,
			status: "unknown", reason: reasonUnknownEngine},
		{name: "the image index of 0.9.0", engine: "v0.9.0",
			image:  "sha256:adf05068581dce841246876dec766d931483134344439beaf12a038c213d32c0",
			action: release09Action, status: "unknown", reason: reasonUnknownEngine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			w.gate.v4.engineApproved, w.gate.v4.actionApproved = manifest.EngineApproved, manifest.ActionApproved
			pinned := workflow4(tc.action, "")
			w.setFile(w.shop, testBranch, ingest.WorkflowPath, pinned)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			s.env.EngineVersion, s.env.ImageDigest = tc.engine, tc.image
			s.result["engine"] = strings.TrimPrefix(tc.engine, "v") // the result names the engine that wrote it
			w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, pinned)
			w.wantStatus(w.send4(s), tc.status, tc.reason)
		})
	}
}
