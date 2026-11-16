package gate

import "sync"

const homeReadConcurrency = 4

// homeReads bounds concurrent record reads, joins all work, and chooses errors
// in input order rather than timing order. No cancellation can mask a rate limit
// or storage error with another read's context.Canceled.
func homeReads(n int, read func(int) error) error {
	errors := make([]error, n)
	for start := 0; start < n; start += homeReadConcurrency {
		var group sync.WaitGroup
		for i := start; i < min(start+homeReadConcurrency, n); i++ {
			group.Go(func() { errors[i] = read(i) })
		}
		group.Wait()
		for i := start; i < min(start+homeReadConcurrency, n); i++ {
			if errors[i] != nil {
				return errors[i]
			}
		}
	}
	return nil
}
