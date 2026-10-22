package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// tokenRoutes manage the caller's own API tokens. They are mounted behind
// auth.RejectToken: tokens are made and revoked by a person in the panel.
func (a *API) tokenRoutes(r chi.Router) {
	r.Get("/", a.handleListTokens)
	// Admins only in v1. Members and viewers can still list and revoke
	// tokens they hold, for example after being demoted.
	r.With(auth.RequireRole("admin")).Post("/", a.handleCreateToken)
	r.Delete("/{id}", a.handleRevokeToken)
}

type tokenDTO struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Prefix         string  `json:"prefix"`
	Scope          string  `json:"scope"`
	CanReadSecrets bool    `json:"can_read_secrets"`
	ExpiresAt      string  `json:"expires_at"`
	Expired        bool    `json:"expired"`
	LastUsedAt     *string `json:"last_used_at"`
	LastUsedIP     *string `json:"last_used_ip"`
	CreatedAt      string  `json:"created_at"`
	RevokedAt      *string `json:"revoked_at"`
}

func toTokenDTO(t db.ApiToken, now time.Time) tokenDTO {
	expired := true
	if exp, err := time.Parse(time.RFC3339, t.ExpiresAt); err == nil {
		expired = !now.Before(exp)
	}
	return tokenDTO{
		ID:             t.ID,
		Name:           t.Name,
		Prefix:         auth.TokenPrefix + t.PublicID,
		Scope:          t.Scope,
		CanReadSecrets: t.CanReadSecrets != 0,
		ExpiresAt:      t.ExpiresAt,
		Expired:        expired,
		LastUsedAt:     nullString(t.LastUsedAt),
		LastUsedIP:     nullString(t.LastUsedIp),
		CreatedAt:      t.CreatedAt,
		RevokedAt:      nullString(t.RevokedAt),
	}
}

func nullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func toTokenDTOs(list []db.ApiToken) []tokenDTO {
	now := time.Now()
	out := make([]tokenDTO, 0, len(list))
	for _, t := range list {
		out = append(out, toTokenDTO(t, now))
	}
	return out
}

func (a *API) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	list, err := a.Queries.ListAPITokensForUser(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, "list api tokens", err)
		return
	}
	writeJSON(w, http.StatusOK, toTokenDTOs(list))
}

func (a *API) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Scope          string `json:"scope"`
		ExpiresInDays  int    `json:"expires_in_days"`
		CanReadSecrets bool   `json:"can_read_secrets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	ttl := auth.DefaultTokenTTL
	if req.ExpiresInDays != 0 {
		ttl = time.Duration(req.ExpiresInDays) * 24 * time.Hour
	}
	user, _ := auth.UserFrom(r.Context())
	plaintext, tok, err := a.Auth.CreateAPIToken(r.Context(), user, req.Name, req.Scope, req.CanReadSecrets, ttl)
	switch {
	case errors.Is(err, auth.ErrInvalidScope):
		writeError(w, http.StatusBadRequest, "bad_request", "scope must be read, write or admin")
		return
	case errors.Is(err, auth.ErrScopeAbove):
		writeError(w, http.StatusForbidden, "forbidden", "a token cannot have more access than its owner")
		return
	case errors.Is(err, auth.ErrInvalidTTL), errors.Is(err, auth.ErrTokenName):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil:
		a.internalError(w, "create api token", err)
		return
	}
	a.Audit.Write(r.Context(), user.ID, "token.create", "api_token", auth.TokenPrefix+tok.PublicID, remoteIP(r),
		map[string]any{"name": tok.Name, "scope": tok.Scope, "can_read_secrets": req.CanReadSecrets,
			"expires_at": tok.ExpiresAt})
	writeJSON(w, http.StatusCreated, struct {
		tokenDTO
		Token string `json:"token"`
	}{toTokenDTO(tok, time.Now()), plaintext})
}

func (a *API) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	a.revokeToken(w, r, user.ID, chi.URLParam(r, "id"))
}

// revokeToken revokes token idParam if it belongs to ownerID. Revoking an
// already revoked token succeeds, so a retry is safe.
func (a *API) revokeToken(w http.ResponseWriter, r *http.Request, ownerID int64, idParam string) {
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	tok, err := a.Queries.GetAPIToken(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && tok.UserID != ownerID {
		writeError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		a.internalError(w, "get api token", err)
		return
	}
	if err := a.Queries.RevokeAPIToken(r.Context(), id); err != nil {
		a.internalError(w, "revoke api token", err)
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), actor.ID, "token.revoke", "api_token", auth.TokenPrefix+tok.PublicID, remoteIP(r),
		map[string]any{"name": tok.Name, "owner_id": tok.UserID})
	w.WriteHeader(http.StatusNoContent)
}

// handleCurrentToken tells a script which token it is using and what it may
// do. It is the one token endpoint a token can call.
func (a *API) handleCurrentToken(w http.ResponseWriter, r *http.Request) {
	info, ok := auth.TokenFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, "not_a_token", "this request is not using an API token")
		return
	}
	tok, err := a.Queries.GetAPIToken(r.Context(), info.ID)
	if err != nil {
		a.internalError(w, "get api token", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, struct {
		tokenDTO
		User userDTO `json:"user"`
	}{toTokenDTO(tok, time.Now()), toUserDTO(user)})
}

func (a *API) handleListUserTokens(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	list, err := a.Queries.ListAPITokensForUser(r.Context(), id)
	if err != nil {
		a.internalError(w, "list api tokens", err)
		return
	}
	writeJSON(w, http.StatusOK, toTokenDTOs(list))
}

func (a *API) handleRevokeUserToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	a.revokeToken(w, r, id, chi.URLParam(r, "tokenID"))
}
