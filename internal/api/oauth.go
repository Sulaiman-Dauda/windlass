package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	oauthSettingPrefix = "oauth."
	oauthStateCookie   = "windlass_oauth_state"
)

// oauthConfig loads a provider's client credentials (stored encrypted).
func (a *API) oauthConfig(ctx context.Context, provider string) (auth.OAuthProviderConfig, error) {
	var cfg auth.OAuthProviderConfig
	raw, err := a.Queries.GetSetting(ctx, oauthSettingPrefix+provider)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var wrapped struct {
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		return cfg, err
	}
	encBytes, err := hex.DecodeString(wrapped.Enc)
	if err != nil {
		return cfg, err
	}
	plain, err := a.Box.Decrypt(encBytes)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(plain, &cfg)
	return cfg, err
}

func (a *API) handleSetOAuthConfig(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if provider != "github" && provider != "google" {
		writeError(w, http.StatusBadRequest, "bad_request", "provider must be github or google")
		return
	}
	var cfg auth.OAuthProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil || !cfg.Configured() {
		writeError(w, http.StatusBadRequest, "bad_request", "client_id and client_secret are required")
		return
	}
	if err := a.saveOAuthConfig(r.Context(), provider, cfg); err != nil {
		a.internalError(w, "save oauth config", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "oauth.configure", "provider", provider, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) saveOAuthConfig(ctx context.Context, provider string, cfg auth.OAuthProviderConfig) error {
	plain, _ := json.Marshal(cfg)
	enc, err := a.Box.Encrypt(plain)
	if err != nil {
		return err
	}
	wrapped, _ := json.Marshal(map[string]string{"enc": hex.EncodeToString(enc)})
	return a.Queries.SetSetting(ctx, db.SetSettingParams{
		Key: oauthSettingPrefix + provider, Value: string(wrapped),
	})
}

func (a *API) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	out := map[string]bool{}
	for _, p := range []string{"github", "google"} {
		cfg, err := a.oauthConfig(r.Context(), p)
		out[p] = err == nil && cfg.Configured()
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) oauthRedirectURI(r *http.Request, provider string) string {
	scheme := "http"
	if isTLS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/v1/auth/oauth/" + provider + "/callback"
}

func (a *API) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	cfg, err := a.oauthConfig(r.Context(), provider)
	if err != nil || !cfg.Configured() {
		writeError(w, http.StatusBadRequest, "not_configured", "this sign-in method is not configured")
		return
	}

	buf := make([]byte, 16)
	rand.Read(buf)
	state := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/api/v1/auth/oauth",
		MaxAge: 600, HttpOnly: true, Secure: isTLS(r), SameSite: http.SameSiteLaxMode,
	})

	target, err := auth.OAuthAuthorizeURL(provider, cfg, a.oauthRedirectURI(r, provider), state, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *API) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	cfg, err := a.oauthConfig(r.Context(), provider)
	if err != nil || !cfg.Configured() {
		http.Redirect(w, r, "/?oauth_error=not_configured", http.StatusFound)
		return
	}

	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") != stateCookie.Value {
		http.Redirect(w, r, "/?oauth_error=state_mismatch", http.StatusFound)
		return
	}

	email, err := auth.OAuthEmail(r.Context(), provider, cfg, a.oauthRedirectURI(r, provider), r.URL.Query().Get("code"))
	if err != nil {
		a.Logger.Warn("oauth callback", "provider", provider, "error", err)
		http.Redirect(w, r, "/?oauth_error=exchange_failed", http.StatusFound)
		return
	}

	// Existing users only: an unknown email is not a signup.
	user, err := a.Queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		a.Audit.Write(r.Context(), 0, "auth.oauth_unknown_user", "user", email, remoteIP(r),
			map[string]string{"provider": provider})
		http.Redirect(w, r, "/?oauth_error=unknown_user", http.StatusFound)
		return
	}

	cookie, err := a.Auth.StartSessionFor(r.Context(), user, remoteIP(r), r.UserAgent())
	if err != nil {
		a.internalError(w, "oauth session", err)
		return
	}
	a.Audit.Write(r.Context(), user.ID, "auth.oauth_login", "user", email, remoteIP(r),
		map[string]string{"provider": provider})
	setSessionCookie(w, r, cookie)
	http.Redirect(w, r, "/", http.StatusFound)
}
