package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthMiddleware enforces a bearer token on the STATEFUL operator surfaces — /api/* and
// /mcp — while leaving liveness (/healthz, /readyz) and /metrics open for kubelet probes
// and Prometheus scraping. The served API is plain HTTP exposing the full
// incident/topology/silence-ledger state (doc 10), so requiring a token once one is set
// — deny-by-default for any exposure beyond loopback — is the in-binary minimum (audit
// roadmap #3).
//
// token == "" => OPEN (the dev default); the caller MUST log that the surfaces are
// unauthenticated. With a token set, a protected request without exactly
// "Authorization: Bearer <token>" gets 401. The comparison is constant-time so a wrong
// token leaks no timing signal.
//
// This is intentionally a minimum: production-grade transport security (mTLS, TLS
// termination, per-caller identity, rate limiting) belongs at an ingress / service mesh
// in front of obsd. The threat model is in docs/testing/security-and-auth.md.
func AuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && RequiresAuth(r.URL.Path) {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			ok := strings.HasPrefix(h, prefix) &&
				subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(token)) == 1
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="vigil-obsd"`)
				http.Error(w, "unauthorized: set 'Authorization: Bearer <token>'", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequiresAuth reports whether a path is a protected stateful surface (/api, /mcp).
// Liveness/readiness and /metrics are deliberately NOT protected (probes + scraping).
func RequiresAuth(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}
