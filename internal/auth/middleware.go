package auth

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/windlass-dev/windlass/internal/store/db"
)

type ctxKey int

const (
	userKey ctxKey = iota
	claimsKey
	tokenKey
)

// Middleware resolves the caller and stores the user in the request context.
// It never rejects an anonymous request, RequireAuth does that, so public
// endpoints can still see who is asking.
//
// A Windlass API token in an Authorization: Bearer header wins over any
// cookie. A bad one is refused here with 401 rather than falling back to
// the cookie, so a script never runs as whoever is signed in to the browser.
// Bearer values without the wl_pat_ prefix are left alone: they belong to
// someone else (a webhook sender, a proxy in front of Windlass).
func Middleware(s *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw, ok := bearerToken(r); ok {
				user, tok, err := s.AuthenticateAPIToken(r.Context(), raw, clientIP(r))
				if err != nil {
					writeAuthError(w, http.StatusUnauthorized, "invalid_token",
						"API token is invalid, expired or revoked")
					return
				}
				ctx := context.WithValue(r.Context(), userKey, user)
				ctx = context.WithValue(ctx, tokenKey, tok)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if c, err := r.Cookie(SessionCookie); err == nil {
				if user, claims, err := s.Authenticate(r.Context(), c.Value); err == nil {
					ctx := context.WithValue(r.Context(), userKey, user)
					ctx = context.WithValue(ctx, claimsKey, claims)
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken returns a Windlass API token from the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, strings.HasPrefix(value, TokenPrefix)
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// UserFrom returns the authenticated user, if any. For an API token the
// role is already capped at the token's scope.
func UserFrom(ctx context.Context) (db.User, bool) {
	u, ok := ctx.Value(userKey).(db.User)
	return u, ok
}

// ClaimsFrom returns the session claims, if any.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}

// roleRank orders roles by privilege for RequireRole.
var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2}

// HasRole reports whether the request's user holds at least role, for
// handlers that shape a response by role rather than refusing it.
func HasRole(ctx context.Context, role string) bool {
	u, ok := UserFrom(ctx)
	return ok && roleRank[u.Role] >= roleRank[role]
}

// RequireAuth rejects unauthenticated requests with 401.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects requests below the given role with 403.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFrom(r.Context())
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			if roleRank[u.Role] < roleRank[role] {
				writeAuthError(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RejectToken refuses API tokens with 403. It guards what only a person at
// a browser should do: managing tokens and users, second factors, sign-in
// providers, the panel domain, self-update, the terminal and project
// deletion. A leaked token can then never mint more tokens, lock the owner
// out, or wipe a project.
func RejectToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := TokenFrom(r.Context()); ok {
			writeAuthError(w, http.StatusForbidden, "session_required",
				"API tokens cannot do this; sign in to the panel")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
