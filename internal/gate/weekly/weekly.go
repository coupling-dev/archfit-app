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

func summarize(entries []entry, now time.Time) counts {
	if entries == nil {
		return counts{}
	}
	c := counts{Known: true}
	for _, e := range entries {
		if !e.At.After(now.Add(-7*24*time.Hour)) || e.At.After(now) {
			continue
		}
		c.Checked++
		switch e.Category {
		case "pass":
			c.Passed++
		case "owner":
			c.Owner++
		}
	}
	return c
}
