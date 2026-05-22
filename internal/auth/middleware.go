package auth

import (
	"context"
	"net/http"
)

type ctxKey int

const userCtxKey ctxKey = 1

type Identity struct {
	UserID int64
	Email  string
}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, userCtxKey, id)
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(userCtxKey).(Identity)
	return id, ok
}

// Required is middleware that rejects unauthenticated requests with 401.
func (s *Signer) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID, email, err := s.Parse(cookie.Value)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := WithIdentity(r.Context(), Identity{UserID: userID, Email: email})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Optional populates identity if a valid cookie is present, but does not reject.
func (s *Signer) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err == nil && cookie.Value != "" {
			if userID, email, err := s.Parse(cookie.Value); err == nil {
				ctx := WithIdentity(r.Context(), Identity{UserID: userID, Email: email})
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}
