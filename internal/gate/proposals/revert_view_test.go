package proposals

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestRevertPreviewIsReadOnlyAndMatchesTheWrittenBytes(t *testing.T) {
	for _, n := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			w := newWorld(t)
			pairs := [][2]string{{"pkg_tc", "pkg_util"}, {"pkg_tc", "pkg_db"}, {"pkg_net", "pkg_util"}, {"pkg_db", "pkg_util"}}
			for i := range n {
				w.request(431+i, "graph", pairs[i][0], pairs[i][1])
				w.mustAnswer(olivia, 431+i, requests.Answer{Kind: policy3.Approve}, true)
			}
			w.outsideTheFlow()
			before, writes := w.snapshot(), len(w.hub.writes())
			v, err := w.svc.RevertPreview(context.Background(), testRepo)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(before, w.snapshot()) || len(w.hub.writes()) != writes {
				t.Fatal("preview wrote state")
			}
			if len(v.Proposals) != min(n, 1) {
				t.Fatalf("proposals = %+v", v.Proposals)
			}
			if n == 0 && !bytes.Equal(v.Result, v.Approved) {
				t.Fatal("plain revert is not the approved file")
			}
			if n > 0 && bytes.Equal(v.Result, v.Approved) {
				t.Fatal("open answers were not shown")
			}
			if _, err := w.revert(oscar); err != nil {
				t.Fatal(err)
			}
			if got := w.hub.file(fakeBase, policyPath); got != string(v.Result) {
				t.Fatalf("written:\n%s\npreviewed:\n%s", got, v.Result)
			}
		})
	}
}
