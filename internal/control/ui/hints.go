package ui

import (
	"container/list"
	"sync"
)

// hintsMax bounds the hints one instance keeps **[default]**: one per pull request page and per repository read
// lately, each some dozens of bytes.
const hintsMax = 4096

// hintKey names one chain of reads: a pull request's, or the default branch's latest report's (pull 0). The
// installation and the repository are part of the key, so one repository's reads never guess with another's values.
type hintKey struct {
	installationID int64
	repo           string
	pull           int
}

// hint is what the last read of a chain found that its next read takes as a parameter: a ref, a SHA, a path, a run
// ID. It holds no answer GitHub gave about them. A read made ahead with it counts only when the fresh read it depends
// on names the same parameter (guess.at), so a stale hint costs a read, never a wrong page.
type hint struct {
	base, head string // a pull request's base branch and head SHA
	workflow   string // the settings' workflow path
	run        int64  // the run the report comes from; 0 when there is none
}

// hints keeps the latest hint of each chain, the least recently used evicted first. It sits behind the per-request
// authorization, as reportCache does.
type hints struct {
	mu    sync.Mutex
	order *list.List // of *keptHint, the most recently used first
	byKey map[hintKey]*list.Element
}

type keptHint struct {
	key  hintKey
	hint hint
}

func newHints() *hints {
	return &hints{order: list.New(), byKey: map[hintKey]*list.Element{}}
}

func (c *hints) get(k hintKey) (hint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	if !ok {
		return hint{}, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*keptHint).hint, true
}

func (c *hints) put(k hintKey, h hint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.byKey[k]; ok {
		e.Value.(*keptHint).hint = h
		c.order.MoveToFront(e)
		return
	}
	c.byKey[k] = c.order.PushFront(&keptHint{k, h})
	if c.order.Len() > hintsMax {
		delete(c.byKey, c.order.Remove(c.order.Back()).(*keptHint).key)
	}
}

// guess is a read made ahead with a hinted parameter, side by side with the read that tells the real one.
type guess[P comparable, T any] struct {
	param P
	made  bool
	val   T
	err   error
}

// ahead returns the read with param, to run beside the read that tells the real parameter.
func (g *guess[P, T]) ahead(param P, read func(P) (T, error)) func() {
	return func() {
		g.param, g.made = param, true
		g.val, g.err = read(param)
	}
}

// at is read's answer for param: the guess's when it was made with param, else a read now.
func (g *guess[P, T]) at(param P, read func(P) (T, error)) (T, error) {
	if g.made && g.param == param {
		return g.val, g.err
	}
	return read(param)
}
