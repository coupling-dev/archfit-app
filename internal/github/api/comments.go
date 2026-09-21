package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"
)

const maxCommentChars = 65536

type commentJSON struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
}

func checkCommentBody(body string) error {
	if utf8.RuneCountInString(body) > maxCommentChars {
		return errors.New("github: comment body over 65536 characters")
	}
	return nil
}

func (c *Client) AppComments(ctx context.Context, r RepoRef, pull int) ([]Comment, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	app, err := c.appIdentity(ctx)
	if err != nil {
		return nil, err
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/issues/{n}/comments",
		url: pagedURL(base+"/issues/"+strconv.Itoa(pull)+"/comments", nil)}
	all, err := listAll[commentJSON](ctx, c, k, "")
	if err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, j := range all {
		if j.User != nil && j.User.Login == app.botLogin {
			out = append(out, Comment{ID: j.ID, Body: j.Body})
		}
	}
	return out, nil
}

func (c *Client) CreateComment(ctx context.Context, r RepoRef, pull int, body string) (Comment, error) {
	if err := checkCommentBody(body); err != nil {
		return Comment{}, err
	}
	cr, base, err := r.target()
	if err != nil {
		return Comment{}, err
	}
	var j commentJSON
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/issues/{n}/comments",
		url: apiURL(base+"/issues/"+strconv.Itoa(pull)+"/comments", nil), body: map[string]string{"body": body}}
	if err := c.send(ctx, k, &j); err != nil {
		return Comment{}, err
	}
	if j.ID == 0 {
		return Comment{}, k.errorf("invalid response")
	}
	return Comment{ID: j.ID, Body: body}, nil
}

func (c *Client) UpdateComment(ctx context.Context, r RepoRef, id int64, body string) error {
	if err := checkCommentBody(body); err != nil {
		return err
	}
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/issues/comments/{id}",
		url: apiURL(base+"/issues/comments/"+itoa(id), nil), body: map[string]string{"body": body}}
	return c.send(ctx, k, nil)
}
