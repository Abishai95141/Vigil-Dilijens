//go:build integration

package e2e

import (
	"net/http"
	"testing"
)

// TestLiveAuth proves the deny-by-default bearer auth in the SHIPPED binary against a
// live cluster: with --api-token set, /api rejects unauthenticated/wrong-token requests
// (401) and accepts the exact token (200), while liveness (/healthz) stays open for
// probes. (The hermetic api/auth_test.go proves the middleware; this proves it is wired
// into obsd and enforced over real HTTP.)
func TestLiveAuth(t *testing.T) {
	kc := kubeconfigPath(t)
	const token = "e2e-secret-token"

	dataDir := t.TempDir()
	obsd := startObsd(t, kc, dataDir, "--api-token", token)

	status := func(path, authz string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, obsd.baseURL+path, nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if s := status("/healthz", ""); s != http.StatusOK {
		t.Errorf("/healthz without token = %d, want 200 (liveness must stay open)", s)
	}
	if s := status("/api/coverage", ""); s != http.StatusUnauthorized {
		t.Errorf("/api/coverage without token = %d, want 401", s)
	}
	if s := status("/api/coverage", "Bearer wrong-token"); s != http.StatusUnauthorized {
		t.Errorf("/api/coverage with wrong token = %d, want 401", s)
	}
	if s := status("/api/coverage", "Bearer "+token); s != http.StatusOK {
		t.Errorf("/api/coverage with correct token = %d, want 200", s)
	}
	t.Logf("live auth OK: /healthz open · /api/coverage 401 (none/wrong) → 200 (correct token)")
}
