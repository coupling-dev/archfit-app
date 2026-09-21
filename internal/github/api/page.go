package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

const (
	perPage  = "100"
	maxItems = 1000
)

// pagedURL is apiURL with per_page=100.
func pagedURL(path string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	q.Set("per_page", perPage)
	return apiURL(path, q)
}

// listPages follows rel="next" from k.url and collects at most limit items. field names the array inside an
// object response; "" means the response is the array. more reports that GitHub holds items beyond limit.
func listPages[T any](ctx context.Context, c *Client, k call, field string, limit int) (items []T, more bool, err error) {
	items = []T{}
	for {
		res, err := c.do(ctx, k)
		if err != nil {
			return nil, false, err
		}
		var page []T
		raw := json.RawMessage(res.body)
		if field != "" {
			var obj map[string]json.RawMessage
			if json.Unmarshal(res.body, &obj) != nil {
				return nil, false, k.errorf("invalid response")
			}
			raw = obj[field]
		}
		if json.Unmarshal(raw, &page) != nil {
			return nil, false, k.errorf("invalid response")
		}
		items = append(items, page...)

		next := nextLink(res.link)
		if len(items) > limit {
			return items[:limit], true, nil
		}
		if next == "" {
			return items, false, nil
		}
		if len(items) == limit {
			return items, true, nil
		}
		u, err := url.Parse(next)
		if err != nil || u.Scheme != "https" || u.Host != apiHost {
			return nil, false, k.errorf("next page link leaves %s", apiHost)
		}
		k.url = next
	}
}

// listAll is listPages for lists that must be complete: beyond the cap it fails, it never truncates.
func listAll[T any](ctx context.Context, c *Client, k call, field string) ([]T, error) {
	items, more, err := listPages[T](ctx, c, k, field, maxItems)
	if err != nil {
		return nil, err
	}
	if more {
		return nil, k.errorf("more than %d items", maxItems)
	}
	return items, nil
}

// nextLink returns the rel="next" target of a Link header, or "".
func nextLink(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for p := range strings.SplitSeq(params, ";") {
			if strings.TrimSpace(p) == `rel="next"` {
				return strings.Trim(target, "<>")
			}
		}
	}
	return ""
}
