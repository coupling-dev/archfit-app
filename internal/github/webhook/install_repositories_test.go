package webhook

import (
	"os"
	"reflect"
	"testing"
)

func TestInstallationAddedRepositories(t *testing.T) {
	for _, tt := range []struct {
		file, event string
		want        []string
	}{
		{"installation_created.json", "installation", []string{"acme/shop"}},
		{"installation_repositories_added.json", "installation_repositories", []string{"acme/shop"}},
	} {
		t.Run(tt.event, func(t *testing.T) {
			body, err := os.ReadFile("testdata/" + tt.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Parse(tt.event, "delivery", body)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.AddedRepositories, tt.want) {
				t.Fatalf("added repositories = %v, want %v", got.AddedRepositories, tt.want)
			}
		})
	}
}
