package policy

import (
	"errors"
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

// Waiver is the subset of an engine waiver the App reads. The engine enforces it; the App only schedules around Expires.
type Waiver struct {
	Rule       string // engine rule ID
	From, To   string // endpoint selectors, may be ""
	Reason     string
	ApprovedBy string    // descriptive only, never authentication
	Expires    time.Time // end of the YYYY-MM-DD day, UTC
}

// waiverEntry is the engine v2.3.1 WaiverDef. Unknown keys are ignored.
type waiverEntry struct {
	Rule       yaml.Node `yaml:"rule"`
	From       yaml.Node `yaml:"from"`
	To         yaml.Node `yaml:"to"`
	Reason     yaml.Node `yaml:"reason"`
	ApprovedBy yaml.Node `yaml:"approved_by"`
	Expires    yaml.Node `yaml:"expires"`
}

// Waivers extracts the waivers of a policy file. It ignores every other key.
func Waivers(policy []byte) ([]Waiver, error) {
	top, ok, err := read(policy)
	if err != nil || !ok {
		return nil, err
	}
	list := resolve(&top.Waivers)
	if isNull(list) {
		return nil, nil
	}
	if list.Kind != yaml.SequenceNode {
		return nil, errors.New("policy: waivers is not a list")
	}
	var out []Waiver
	for i, item := range list.Content {
		w, err := readWaiver(i, resolve(item))
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func readWaiver(i int, item *yaml.Node) (Waiver, error) {
	if item.Kind != yaml.MappingNode {
		return Waiver{}, fmt.Errorf("policy: waivers[%d] is not a mapping", i)
	}
	var entry waiverEntry
	if err := item.Decode(&entry); err != nil {
		return Waiver{}, fmt.Errorf("policy: waivers[%d] is not readable", i)
	}

	var w Waiver
	var expires string
	fields := []struct {
		key  string
		node *yaml.Node
		dst  *string
	}{
		{"rule", &entry.Rule, &w.Rule},
		{"from", &entry.From, &w.From},
		{"to", &entry.To, &w.To},
		{"reason", &entry.Reason, &w.Reason},
		{"approved_by", &entry.ApprovedBy, &w.ApprovedBy},
		{"expires", &entry.Expires, &expires},
	}
	for _, f := range fields {
		text, ok := scalarText(f.node)
		if !ok {
			return Waiver{}, fmt.Errorf("policy: waivers[%d].%s is not a scalar", i, f.key)
		}
		*f.dst = text
	}

	// The format round trip rejects anything time.Parse reads leniently; only exact YYYY-MM-DD passes.
	day, err := time.Parse(time.DateOnly, expires)
	if err != nil || day.Format(time.DateOnly) != expires {
		return Waiver{}, fmt.Errorf("policy: waivers[%d].expires is not a YYYY-MM-DD date", i)
	}
	w.Expires = day.Add(24*time.Hour - time.Second)
	return w, nil
}
