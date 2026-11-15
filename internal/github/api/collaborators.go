package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// CollaboratorPermission returns the permission of login on the repository, read with the installation token:
// "admin", "write", "read" or "none". A login GitHub does not know is "none". GitHub reports the maintain role as
// "write" and the triage role as "read".
func (c *Client) CollaboratorPermission(ctx context.Context, r RepoRef, login string) (string, error) {
	if login == "" {
		return "", errors.New("github: empty login")
	}
	cr, base, err := r.target()
	if err != nil {
		return "", err
	}
	var j struct {
		Permission string `json:"permission"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/collaborators/{login}/permission",
		url: apiURL(base+"/collaborators/"+url.PathEscape(login)+"/permission", nil)}
	if err := c.send(ctx, k, &j); err != nil {
		if errors.Is(err, ErrNotFound) {
			return permissionNone, nil
		}
		return "", err
	}
	switch j.Permission {
	case "admin", "write", "read":
		return j.Permission, nil
	}
	return permissionNone, nil
}

const permissionNone = "none"
