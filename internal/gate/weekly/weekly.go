package weekly

import "time"

type counts struct {
	Checked, Passed, Owner int
	Known                  bool
}

type entry struct {
	At       time.Time
	Category string
}

func summarize(entries []entry, now time.Time) counts { return counts{} }
