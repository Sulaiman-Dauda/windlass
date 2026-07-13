package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func (a *API) userRoutes(r chi.Router) {
	r.Use(auth.RequireRole("admin"))
	r.Get("/", a.handleListUsers)
	r.Post("/", a.handleCreateUser)
	r.Put("/{id}/role", a.handleSetUserRole)
	r.Delete("/{id}", a.handleDeleteUser)
}

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.Queries.ListUsers(r.Context())
	if err != nil {
		a.internalError(w, "list users", err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "email": u.Email, "role": u.Role,
			"totp_enabled": u.TotpEnabled != 0,
			"oauth":        u.OauthProvider.String,
			"disabled":     u.DisabledAt.Valid,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "bad_request", "valid email required")
		return
	}
	switch req.Role {
	case "admin", "member", "viewer":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "role must be admin, member, or viewer")
		return
	}

	var hash sql.NullString
	if req.Password != "" {
		if len(req.Password) < 10 {
			writeError(w, http.StatusBadRequest, "bad_request", "password must be at least 10 characters")
			return
		}
		h, err := auth.HashPassword(req.Password)
		if err != nil {
			a.internalError(w, "hash password", err)
			return
		}
		hash = sql.NullString{String: h, Valid: true}
	}
	// Without a password the account can only sign in via OAuth.

	u, err := a.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Email: req.Email, PasswordHash: hash, Role: req.Role,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "conflict", "a user with this email already exists")
			return
		}
		a.internalError(w, "create user", err)
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), actor.ID, "user.create", "user", u.Email, remoteIP(r),
		map[string]string{"role": u.Role})
	writeJSON(w, http.StatusCreated, toUserDTO(u))
}

func (a *API) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	switch req.Role {
	case "admin", "member", "viewer":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "invalid role")
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	if actor.ID == id && req.Role != "admin" {
		writeError(w, http.StatusBadRequest, "bad_request", "you cannot demote yourself")
		return
	}
	if err := a.Queries.UpdateUserRole(r.Context(), db.UpdateUserRoleParams{Role: req.Role, ID: id}); err != nil {
		a.internalError(w, "set role", err)
		return
	}
	a.Audit.Write(r.Context(), actor.ID, "user.role", "user", chi.URLParam(r, "id"), remoteIP(r),
		map[string]string{"role": req.Role})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	if actor.ID == id {
		writeError(w, http.StatusBadRequest, "bad_request", "you cannot delete yourself")
		return
	}
	if err := a.Queries.DeleteUser(r.Context(), id); err != nil {
		a.internalError(w, "delete user", err)
		return
	}
	a.Audit.Write(r.Context(), actor.ID, "user.delete", "user", chi.URLParam(r, "id"), remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}
