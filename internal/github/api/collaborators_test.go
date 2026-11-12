package api

import (
	"context"
	"net/http"
	"testing"
)

func TestCollaboratorPermission(t *testing.T) {
	ctx := context.Background()
	env := muxEnv(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /repos/o/r/collaborators/{login}/permission", func(w http.ResponseWriter, r *http.Request) {
			switch login := r.PathValue("login"); login {
			case "ghost":
				writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			case "hidden":
				writeJSON(w, http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"})
			case "odd":
				writeJSON(w, http.StatusOK, map[string]any{"permission": "maintain"})
			default:
				writeJSON(w, http.StatusOK, map[string]any{"permission": login, "role_name": login})
			}
		})
	})

	tests := []struct{ login, want string }{
		{"admin", "admin"},
		{"write", "write"},
		{"read", "read"},
		{"none", "none"},
		{"ghost", "none"},
		{"odd", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.login, func(t *testing.T) {
			got, err := env.c.CollaboratorPermission(ctx, repoA, tt.login)
			if err != nil || got != tt.want {
				t.Errorf("CollaboratorPermission(%s) = %q, %v; want %q", tt.login, got, err, tt.want)
			}
		})
	}
	req := findRequest(t, env.gh.requests(), "GET", "/repos/o/r/collaborators/write/permission")
	if req.Auth != inst1 {
		t.Errorf("Authorization = %q, want the installation token", req.Auth)
	}

	if _, err := env.c.CollaboratorPermission(ctx, repoA, "hidden"); Status(err) != http.StatusForbidden {
		t.Errorf("a 403 = %v, want the error with its status", err)
	}
	if _, err := env.c.CollaboratorPermission(ctx, repoA, ""); err == nil {
		t.Error("an empty login is not an error")
	}
}
