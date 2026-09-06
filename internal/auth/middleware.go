package auth

import (
	"context"
	"net/http"

	"github.com/windlass-dev/windlass/internal/store/db"
)

type ctxKey int

const (
	userKey ctxKey = iota
	claimsKey
)

// Middleware resolves the session cookie (if any) and stores the user in the
// request context. It never rejects, RequireAuth does that, so public
// endpoints can still see who is asking.
func Middleware(s *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// UserFrom returns the authenticated user, if any.
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

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
