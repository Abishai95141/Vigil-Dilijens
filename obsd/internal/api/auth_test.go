package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAuthMiddleware proves the deny-by-default contract on the stateful surfaces:
// with a token configured, /api and /mcp reject anything but the exact bearer token,
// while liveness and /metrics stay open; with no token, everything is open (the dev
// default, which the runtime warns about loudly).
func TestAuthMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	for _, p := range []string{"/api/coverage", "/mcp", "/healthz", "/metrics"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	const token = "s3cr3t-token"

	cases := []struct {
		name, path, authz, token string
		want                     int
	}{
		{"no-token-api-open", "/api/coverage", "", "", http.StatusOK}, // dev default
		{"token-missing-header-401", "/api/coverage", "", token, http.StatusUnauthorized},
		{"token-wrong-401", "/api/coverage", "Bearer nope", token, http.StatusUnauthorized},
		{"token-no-bearer-prefix-401", "/api/coverage", token, token, http.StatusUnauthorized},
		{"token-correct-200", "/api/coverage", "Bearer " + token, token, http.StatusOK},
		{"mcp-protected-401", "/mcp", "", token, http.StatusUnauthorized},
		{"mcp-correct-200", "/mcp", "Bearer " + token, token, http.StatusOK},
		{"healthz-always-open", "/healthz", "", token, http.StatusOK}, // liveness never gated
		{"metrics-always-open", "/metrics", "", token, http.StatusOK}, // scraping never gated
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(AuthMiddleware(c.token, mux))
			defer srv.Close()
			req, _ := http.NewRequest(http.MethodGet, srv.URL+c.path, nil)
			if c.authz != "" {
				req.Header.Set("Authorization", c.authz)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("GET %s (token=%q authz=%q): status %d, want %d", c.path, c.token, c.authz, resp.StatusCode, c.want)
			}
		})
	}
}

func TestRequiresAuth(t *testing.T) {
	for _, p := range []string{"/api", "/api/coverage", "/api/findings", "/mcp", "/mcp/x"} {
		if !RequiresAuth(p) {
			t.Errorf("%s should be protected", p)
		}
	}
	for _, p := range []string{"/healthz", "/readyz", "/metrics", "/", "/apix", "/mcpx"} {
		if RequiresAuth(p) {
			t.Errorf("%s should be open", p)
		}
	}
}
