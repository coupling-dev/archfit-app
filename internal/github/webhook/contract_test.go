package webhook

import (
	"reflect"
	"testing"
)

func TestVerifyThenParseRealDeliveries(t *testing.T) {
	secret := []byte(testSecret)
	for _, tt := range fixtureCases {
		t.Run(tt.file, func(t *testing.T) {
			// Signed over the bytes as read: a formatter may rewrite the fixture file.
			payload := fixture(t, tt.file)
			if err := Verify(secret, sign(secret, payload), payload); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			got, err := Parse(tt.eventType, testDelivery, payload)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			want := tt.want
			want.DeliveryID = testDelivery
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse() =\n %+v, want\n %+v", got, want)
			}
		})
	}
}

func TestUnknownEventTypeIsIgnoredNotError(t *testing.T) {
	bodies := map[string][]byte{
		"valid JSON": body(t, prPayload("opened", shaA)),
		"empty JSON": []byte(`{}`),
		"non-JSON":   []byte("not json <at all>"),
	}
	for _, eventType := range []string{"star", "workflow_run", "check_run"} {
		for name, payload := range bodies {
			t.Run(eventType+"/"+name, func(t *testing.T) {
				got, err := Parse(eventType, testDelivery, payload)
				if err != nil {
					t.Fatalf("Parse() error = %v", err)
				}
				// A zero InstallationID under a body that has one proves the body was not parsed.
				want := Event{Kind: Ignored, DeliveryID: testDelivery}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("Parse() = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestForkDetectionFields(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantHead string
		wantFork bool
	}{
		{"same-repository PR", "pull_request_opened.json", "acme/shop", false},
		{"fork PR", "pull_request_synchronize.json", "octocat/shop", true},
		{"fork PR with deleted head repository", "pull_request_closed.json", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Parse("pull_request", testDelivery, fixture(t, tt.file))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if ev.HeadRepo != tt.wantHead {
				t.Errorf("HeadRepo = %q, want %q", ev.HeadRepo, tt.wantHead)
			}
			if fork := ev.HeadRepo != ev.Repo; fork != tt.wantFork {
				t.Errorf("HeadRepo != Repo is %v, want %v (HeadRepo %q, Repo %q)", fork, tt.wantFork, ev.HeadRepo, ev.Repo)
			}
		})
	}
}
