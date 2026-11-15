package ui

import (
	"net/http"
	"strconv"
)

// pullNumber reads the {number} path value: a positive integer, or not a pull request.
func pullNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	return number, err == nil && number > 0
}
