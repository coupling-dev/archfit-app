package records

import (
	"testing"
)

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
