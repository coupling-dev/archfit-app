package requests

import (
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/wire/request"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// The kinds of a record's next step (wire/result) and the engine's step that asks an owner.
const (
	nextCommand    = "command"
	nextWait       = "wait"
	requestCommand = "archfit request"
	waitPrefix     = "wait for an owner: #"
	statusPrefix   = "archfit: waiting, "
)

// summaryCap is the number of requests the status line names before it counts the rest.
const summaryCap = 10

// Asks reports whether an error asks an owner: its next step is the engine's request command.
func Asks(e result.Error) bool {
	return e.Next.Kind == nextCommand && strings.Contains(e.Next.Text, requestCommand)
}

// Key returns the request key of an error (wire/request).
func Key(e result.Error) (string, error) {
	return request.Error{Rule: e.Rule, Path: e.Path, From: e.From, To: e.To, Key: e.Key}.RequestKey()
}

// Cover derives the status waiting (design 7.1): a fail whose errors all ask an owner, and whose keys are all open
// requests of open, is waiting. The result it returns says so in the status line, "archfit: waiting, request #431",
// and each error's next step names its request. ids are the requests, in record order, once each. Any other result is
// returned unchanged with no ids.
func Cover(r result.Result, open *records.Index) (shown result.Result, ids []int) {
	if r.Status != result.StatusFail || len(r.Errors) == 0 || len(r.Unknown) > 0 || open == nil {
		return r, nil
	}
	errs := slices.Clone(r.Errors)
	for i, e := range r.Errors {
		if !Asks(e) {
			return r, nil
		}
		key, err := Key(e)
		if err != nil {
			return r, nil
		}
		id, found := open.Lookup(key)
		n, err := strconv.Atoi(id)
		if !found || err != nil || n <= 0 {
			return r, nil
		}
		errs[i].Next = result.Next{Kind: nextWait, Text: waitPrefix + strconv.Itoa(n)}
		if !slices.Contains(ids, n) {
			ids = append(ids, n)
		}
	}
	shown = r
	shown.Status, shown.Summary, shown.Errors = result.StatusWaiting, statusPrefix+requestNames(ids), errs
	return shown, ids
}

// requestNames is "request #431", "requests #431 and #432", or "requests #1, …, #10 and 5 more".
func requestNames(ids []int) string {
	names := make([]string, min(len(ids), summaryCap))
	for i := range names {
		names[i] = "#" + strconv.Itoa(ids[i])
	}
	switch {
	case len(ids) == 1:
		return "request " + names[0]
	case len(ids) > summaryCap:
		return "requests " + strings.Join(names, ", ") + " and " + strconv.Itoa(len(ids)-summaryCap) + " more"
	}
	return "requests " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
