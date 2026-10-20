package ui

import "sync"

// together runs fns side by side and returns when all have. A panic in one is raised again on the caller's goroutine
// once the others are done, so the request's recover still answers 500 and the process keeps serving. onboarding keeps
// the same helper: two small copies cost less than a package of their own.
func together(fns ...func()) {
	var (
		wg     sync.WaitGroup
		once   sync.Once
		thrown any
	)
	for _, fn := range fns {
		wg.Go(func() {
			defer func() {
				if p := recover(); p != nil {
					once.Do(func() { thrown = p })
				}
			}()
			fn()
		})
	}
	wg.Wait()
	if thrown != nil {
		panic(thrown)
	}
}
