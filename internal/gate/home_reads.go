package gate

import "sync"

const homeReadConcurrency = 4

// homeReads runs every read and joins them all, then chooses the error in input order rather than timing order.
// The reads share the request's bound (see homeViews.bounded), so no batching happens here. No cancellation can
// mask a rate limit or storage error with another read's context.Canceled.
func homeReads(n int, read func(int) error) error {
	errs := make([]error, n)
	var group sync.WaitGroup
	for i := range n {
		group.Go(func() { errs[i] = read(i) })
	}
	group.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
