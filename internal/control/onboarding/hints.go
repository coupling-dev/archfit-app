package onboarding

import (
	"container/list"
	"sync"
)

// hintsMax bounds the hints one instance keeps **[default]**: one per repository whose progress was read lately, each
// some dozens of bytes.
const hintsMax = 4096

// hintKey names one repository. The installation is part of the key, so one installation's repository never reads
// ahead with another's values under the same name.
type hintKey struct {
	installationID int64
	repo           string
}

// hint is what the last derive found that the next one takes as a parameter: the settings' workflow path and the
// head SHA of that workflow's latest completed push run. It holds no answer GitHub gave about them. A read made
// ahead with it counts only when the fresh read it depends on names the same parameter (guess.at), so a stale hint
// costs a read, never a wrong stage.
type hint struct {
	workflow string
	head     string // "" when the workflow had no run
}

// hints keeps the latest hint of each repository, the least recently used evicted first. control/ui keeps the same
// store for its pages: two small copies cost less than a package of their own.
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

// drop forgets k's hint once the fresh reads say there is nothing to read ahead, so a gone chain stops costing reads.
func (c *hints) drop(k hintKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.byKey[k]; ok {
		c.order.Remove(e)
		delete(c.byKey, k)
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
