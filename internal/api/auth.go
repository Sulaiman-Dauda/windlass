package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/store/db"
)

type userDTO struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func toUserDTO(u db.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Role: u.Role}
}

type authStatusResponse struct {
	NeedsSetup    bool     `json:"needs_setup"`
	Authenticated bool     `json:"authenticated"`
	User          *userDTO `json:"user,omitempty"`
}

func (a *API) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	needsSetup, err := a.Auth.NeedsSetup(r.Context())
	if err != nil {
		a.Logger.Error("auth status", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "failed to read auth state")
		return
	}
	resp := authStatusResponse{NeedsSetup: needsSetup}
	if user, ok := auth.UserFrom(r.Context()); ok {
		resp.Authenticated = true
		dto := toUserDTO(user)
		resp.User = &dto
	}
	writeJSON(w, http.StatusOK, resp)
}

type setupRequest struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "bad_request", "valid email required")
		return
	}

	cookie, user, err := a.Auth.Setup(r.Context(), strings.TrimSpace(req.Token), req.Email, req.Password, remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, auth.ErrSetupComplete):
		writeError(w, http.StatusForbidden, "setup_complete", "instance is already set up")
		return
	case errors.Is(err, auth.ErrSetupToken):
		a.Audit.Write(r.Context(), 0, "auth.setup_failed", "", "", remoteIP(r), nil)
		writeError(w, http.StatusForbidden, "invalid_token", "invalid setup token")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	a.Audit.Write(r.Context(), user.ID, "auth.setup", "user", user.Email, remoteIP(r), nil)
	setSessionCookie(w, r, cookie)
	w.WriteHeader(http.StatusNoContent)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	cookie, user, err := a.Auth.Login(r.Context(), strings.TrimSpace(req.Email), req.Password, strings.TrimSpace(req.TOTPCode), remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, auth.ErrTOTPRequired):
		writeError(w, http.StatusUnauthorized, "totp_required", "enter your authenticator code")
		return
	case errors.Is(err, auth.ErrTOTPInvalid):
		a.Audit.Write(r.Context(), 0, "auth.totp_failed", "user", req.Email, remoteIP(r), nil)
		writeError(w, http.StatusUnauthorized, "totp_invalid", "invalid authenticator code")
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		a.Audit.Write(r.Context(), 0, "auth.login_failed", "user", req.Email, remoteIP(r), nil)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	case err != nil:
		a.Logger.Error("login", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}

	a.Audit.Write(r.Context(), user.ID, "auth.login", "user", user.Email, remoteIP(r), nil)
	setSessionCookie(w, r, cookie)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if claims, ok := auth.ClaimsFrom(r.Context()); ok {
		if err := a.Auth.Logout(r.Context(), claims.SessionID); err != nil {
			a.Logger.Error("logout", "error", err)
		}
		if user, ok := auth.UserFrom(r.Context()); ok {
			a.Audit.Write(r.Context(), user.ID, "auth.logout", "user", user.Email, remoteIP(r), nil)
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	dto := toUserDTO(user)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": dto.ID, "email": dto.Email, "role": dto.Role,
		"totp_enabled": user.TotpEnabled != 0,
		// OAuth-only accounts have no password; the UI uses this to decide
		// whether destructive actions can ask for one.
		"has_password": user.PasswordHash.Valid,
	})
}

// ---------------------------------------------------------------------------
// TOTP enrollment (self-service)

func (a *API) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	secret, url, err := a.Auth.BeginTOTPEnrollment(r.Context(), user)
	if err != nil {
		a.internalError(w, "totp setup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": url})
}

func (a *API) handleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if err := a.Auth.VerifyTOTPEnrollment(r.Context(), user, strings.TrimSpace(req.Code)); err != nil {
		writeError(w, http.StatusBadRequest, "totp_invalid", err.Error())
		return
	}
	a.Audit.Write(r.Context(), user.ID, "auth.totp_enabled", "user", user.Email, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	if err := a.Auth.DisableTOTP(r.Context(), user); err != nil {
		a.internalError(w, "totp disable", err)
		return
	}
	a.Audit.Write(r.Context(), user.ID, "auth.totp_disabled", "user", user.Email, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.SessionTTL().Seconds()),
		HttpOnly: true,
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func remoteIP(r *http.Request) string {
	// chi middleware.RealIP has already normalized RemoteAddr.
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	return host
}
