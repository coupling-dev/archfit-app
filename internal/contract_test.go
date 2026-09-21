package internal_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestUploadProtocolAcceptsApprovedProducer(t *testing.T) {
	f := loadFixture(t, "real-healthy")
	w := newWorld(t)
	w.seedDemo(f, "required", true)
	a := startApp(t, w)

	got := readAnswer(t, a.do(demoUpload(f).request(t, "/v1/reports")))
	if got.Status/100 != 2 || got.Conclusion != "success" {
		t.Fatalf("upload answered %+v; want 2xx success", got)
	}

	gateCheck := w.check(demoRepo, demoHead, checkGate)
	if gateCheck == nil || gateCheck.Status != "completed" || gateCheck.Conclusion != "success" {
		t.Fatalf("archfit/gate = %+v; want completed success", gateCheck)
	}
	if state := w.check(demoRepo, demoHead, checkState); state == nil || state.Status != "completed" {
		t.Fatalf("archfit/state = %+v; want a completed check", state)
	}
	rc := gateCheck.receipt(t)
	want := receipt{
		RunID: demoRun, RunAttempt: 1, ReportDigest: "sha256:" + digest(f.report), PolicyDigest: digest(f.policy),
		BaselineDigest: digest(f.baseline), Conclusion: "success", Reason: "healthy",
	}
	if rc != want {
		t.Errorf("receipt = %+v\nwant      %+v", rc, want)
	}
	comments := w.commentsOf(demoRepo, demoPull)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, commentMarker) {
		t.Fatalf("comments = %+v; want one carrying the marker", comments)
	}
	if published := w.everythingPublished(); !strings.Contains(published, "customer-attested") || strings.Contains(strings.ToLower(published), "verified") {
		t.Error(`feedback must say "customer-attested" and never "verified"`)
	}
	if http.DefaultTransport != router {
		t.Fatal("the network guard was replaced")
	}
}
