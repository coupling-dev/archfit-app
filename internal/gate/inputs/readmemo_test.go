package inputs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestMemoReadsOnceForAllItsCallers(t *testing.T) {
	const callers = 8
	ctx := WithReadMemo(context.Background())
	var loads atomic.Int32
	var entered sync.WaitGroup
	entered.Add(callers)
	release := make(chan struct{})
	var callersDone sync.WaitGroup
	results := make([]string, callers)
	for i := range callers {
		callersDone.Go(func() {
			entered.Done()
			v, err := Memo(ctx, "same", func() (string, error) {
				loads.Add(1)
				// The owner's read returns only once every caller has entered, so the others wait on it.
				entered.Wait()
				<-release
				return "bytes", nil
			})
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
			results[i] = v
		})
	}
	close(release)
	callersDone.Wait()
	if got := loads.Load(); got != 1 {
		t.Fatalf("read ran %d times, want once", got)
	}
	for i, v := range results {
		if v != "bytes" {
			t.Fatalf("caller %d got %q", i, v)
		}
	}
}

func TestMemoKeepsAFailureForTheRequest(t *testing.T) {
	ctx := WithReadMemo(context.Background())
	boom := errors.New("github is down")
	var loads int
	for range 2 {
		_, err := Memo(ctx, "k", func() (int, error) {
			loads++
			return 0, boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
	}
	if loads != 1 {
		t.Fatalf("read ran %d times, want once", loads)
	}
}

func TestMemoCallsEveryReadWithoutAMemo(t *testing.T) {
	var loads int
	for range 2 {
		if _, err := Memo(context.Background(), "k", func() (int, error) { loads++; return loads, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 {
		t.Fatalf("reads = %d, want 2 without a memo", loads)
	}
}

func TestMemoKeepsKeysApart(t *testing.T) {
	ctx := WithReadMemo(context.Background())
	pulls := map[PullRead]int{}
	for _, n := range []int{7, 8, 7} {
		key := PullRead{Number: n}
		if _, err := Memo(ctx, key, func() (int, error) { pulls[key]++; return n, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if pulls[PullRead{Number: 7}] != 1 || pulls[PullRead{Number: 8}] != 1 {
		t.Fatalf("reads = %v, want one per pull request", pulls)
	}
}

func TestMemoFailsClosedWhenTheReadDidNotReturn(t *testing.T) {
	ctx := WithReadMemo(context.Background())
	func() {
		defer func() { _ = recover() }()
		_, _ = Memo(ctx, "k", func() (int, error) { panic("read crashed") })
	}()
	var loads int
	_, err := Memo(ctx, "k", func() (int, error) { loads++; return 1, nil })
	if !errors.Is(err, errNoResult) || loads != 0 {
		t.Fatalf("after a crashed read: err = %v, loads = %d", err, loads)
	}
}

func TestMemoRefusesAKeyHeldWithAnotherType(t *testing.T) {
	ctx := WithReadMemo(context.Background())
	if _, err := Memo(ctx, "k", func() (string, error) { return "text", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := Memo(ctx, "k", func() (int, error) { return 1, nil }); !errors.Is(err, errKeyType) {
		t.Fatalf("err = %v, want %v", err, errKeyType)
	}
}

func TestInstallingTheMemoTwiceKeepsTheFirst(t *testing.T) {
	ctx := WithReadMemo(context.Background())
	again := WithReadMemo(ctx)
	if again != ctx {
		t.Fatal("a second memo replaced the first")
	}
}

func TestMemoStoreReadsEachKeyOncePerRequest(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.Put(ctx, 7, "record", []byte("one")); err != nil {
		t.Fatal(err)
	}
	counted := &countingStore{Store: mem, get: func(int64) ([]byte, error) { return mem.Get(ctx, 7, "record") }}
	s := MemoStore(counted)
	memo := WithReadMemo(ctx)
	for range 3 {
		raw, err := s.Get(memo, 7, "record")
		if err != nil || string(raw) != "one" {
			t.Fatalf("get = %q, %v", raw, err)
		}
	}
	if reads := counted.reads.Load(); reads != 1 {
		t.Fatalf("store reads = %d, want 1 in the request", reads)
	}
	if err := s.Put(memo, 7, "record", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if raw, _ := mem.Get(ctx, 7, "record"); string(raw) != "two" {
		t.Fatalf("write did not reach the store: %q", raw)
	}
}
