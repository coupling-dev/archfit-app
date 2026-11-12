package request_test

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

// TestGoldensUseNoNotUsedWord: no golden uses a word of the "Not used" column of design section 5.
func TestGoldensUseNoNotUsedWord(t *testing.T) {
	for _, path := range allGoldens(t) {
		if found := wording.NotUsedIn(string(read(t, path))); len(found) > 0 {
			t.Errorf("%s uses %q", path, found)
		}
	}
}
