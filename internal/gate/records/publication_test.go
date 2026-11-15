package records

import "testing"

func TestPublicationRoundTrip(t *testing.T) {
	want := Publication{HeadSHA: "head", RunID: 3, RunAttempt: 2, Result: []byte(`{"status":"unknown"}`), Digest: "sha256:abc", Source: "sweep"}
	raw, err := Encode(&want)
	if err != nil {
		t.Fatal(err)
	}
	var got Publication
	if err := Decode(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != want.HeadSHA || got.RunID != want.RunID || got.RunAttempt != want.RunAttempt || string(got.Result) != string(want.Result) || got.Digest != want.Digest || got.Source != want.Source {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPullWithoutPublicationStillDecodes(t *testing.T) {
	var pull Pull
	if err := Decode([]byte(`{"schema":"archfit-app.pull.v1","head_sha":"head","run_id":7,"run_attempt":1,"payload_digest":"sha256:old","status":"pass"}`), &pull); err != nil {
		t.Fatal(err)
	}
	if pull.RunID != 7 || pull.Status != "pass" {
		t.Fatalf("pull %+v", pull)
	}
}
