package middleware

import (
	"net/http"
	"strings"

	"github.com/go-ledger/internal/http/handlers"
	"github.com/go-ledger/internal/ledger"
)

func Auth(svc *ledger.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				handlers.WriteErr(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			raw := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			if raw == "" {
				handlers.WriteErr(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			uid, role, kid, prefix, err := svc.Authenticate(r.Context(), raw)
			if err != nil {
				handlers.WriteErr(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
				return
			}
			ctx := ledger.WithCaller(r.Context(), uid, role, kid, prefix)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
