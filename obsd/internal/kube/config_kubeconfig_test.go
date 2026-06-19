package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the kubeconfig RESOLUTION ORDER documented on NewClientset /
// restConfig (doc 14 A2: dev runs out-of-cluster via kubeconfig). They are fully
// hermetic: every probe uses an on-disk kubeconfig under t.TempDir(); the
// in-cluster branch is forced OFF by clearing KUBERNETES_SERVICE_HOST/PORT so a
// real cluster (or CI that happens to set those) can never leak in and change the
// resolved config. No network: BuildConfigFromFlags / the deferred loader only
// PARSE the file — they do not dial the server.

// writeKubeconfig writes a minimal but complete kubeconfig naming the given server
// and bearer token, returning the path. The two parameters let a test prove WHICH
// file won precedence (distinct servers => distinct resolved Host).
func writeKubeconfig(t *testing.T, dir, name, server, token string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"- name: c\n" +
		"  cluster:\n" +
		"    server: " + server + "\n" +
		"contexts:\n" +
		"- name: ctx\n" +
		"  context:\n" +
		"    cluster: c\n" +
		"    user: u\n" +
		"current-context: ctx\n" +
		"users:\n" +
		"- name: u\n" +
		"  user:\n" +
		"    token: " + token + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write kubeconfig %s: %v", name, err)
	}
	return path
}

// notInCluster blanks the env that rest.InClusterConfig() keys on, so the
// in-cluster branch of restConfig deterministically returns ErrNotInCluster and
// the resolution proceeds to the default loading rules.
func notInCluster(t *testing.T) {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
}

func TestRestConfig_ExplicitKubeconfigWins(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	// A DIFFERENT file is pointed at by KUBECONFIG; the explicit arg must beat it.
	explicit := writeKubeconfig(t, dir, "explicit", "https://explicit.test:6443", "tok-explicit")
	deflt := writeKubeconfig(t, dir, "default", "https://default.test:6443", "tok-default")
	t.Setenv("KUBECONFIG", deflt)

	cfg, err := restConfig(explicit)
	if err != nil {
		t.Fatalf("restConfig(explicit): %v", err)
	}
	if cfg.Host != "https://explicit.test:6443" {
		t.Fatalf("explicit kubeconfig did not win: Host = %q, want explicit.test", cfg.Host)
	}
	if cfg.BearerToken != "tok-explicit" {
		t.Fatalf("explicit kubeconfig token not loaded: BearerToken = %q", cfg.BearerToken)
	}
}

func TestRestConfig_FallsBackToKUBECONFIGEnv(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	// Empty kubeconfig arg + no in-cluster => default loading rules read KUBECONFIG.
	envPath := writeKubeconfig(t, dir, "env", "https://from-env.test:6443", "tok-env")
	t.Setenv("KUBECONFIG", envPath)

	cfg, err := restConfig("")
	if err != nil {
		t.Fatalf("restConfig(\"\"): %v", err)
	}
	if cfg.Host != "https://from-env.test:6443" {
		t.Fatalf("KUBECONFIG env not honoured: Host = %q, want from-env.test", cfg.Host)
	}
	if cfg.BearerToken != "tok-env" {
		t.Fatalf("KUBECONFIG token not loaded: BearerToken = %q", cfg.BearerToken)
	}
}

// TestRestConfig_KUBECONFIGListPrecedence proves the loader honours the colon-list
// precedence of KUBECONFIG (first file's current-context wins for the merged value),
// guarding against a regression that ignored the list ordering.
func TestRestConfig_KUBECONFIGListPrecedence(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	first := writeKubeconfig(t, dir, "first", "https://first.test:6443", "tok-first")
	second := writeKubeconfig(t, dir, "second", "https://second.test:6443", "tok-second")
	t.Setenv("KUBECONFIG", first+string(os.PathListSeparator)+second)

	cfg, err := restConfig("")
	if err != nil {
		t.Fatalf("restConfig(\"\"): %v", err)
	}
	if cfg.Host != "https://first.test:6443" {
		t.Fatalf("KUBECONFIG list precedence wrong: Host = %q, want first.test", cfg.Host)
	}
}

func TestRestConfig_MissingFileIsError(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")

	cfg, err := restConfig(missing)
	if err == nil {
		t.Fatalf("restConfig of a missing file returned nil error (cfg=%v)", cfg)
	}
	if cfg != nil {
		t.Fatalf("restConfig error path must return nil config, got %v", cfg)
	}
	// The error must name the failed file so an operator can see WHICH path broke.
	if !strings.Contains(err.Error(), "load kubeconfig") {
		t.Fatalf("error not wrapped with context: %v", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error omits the offending path: %v", err)
	}
}

func TestRestConfig_MalformedFileIsError(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken")
	// Valid path, invalid contents: must surface as an error, never a zero config.
	if err := os.WriteFile(bad, []byte("\x00\x01not a kubeconfig: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := restConfig(bad)
	if err == nil {
		t.Fatalf("restConfig of a malformed file returned nil error (cfg=%v)", cfg)
	}
	if cfg != nil {
		t.Fatalf("restConfig error path must return nil config, got %v", cfg)
	}
}

// TestNewClientset_BuildsFromKubeconfig is the end-to-end constructor: a valid file
// must yield a non-nil clientset (no dial happens at construction), and a missing
// file must propagate the restConfig error rather than panic or return a half-built
// clientset.
func TestNewClientset_BuildsFromKubeconfig(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	good := writeKubeconfig(t, dir, "good", "https://good.test:6443", "tok")

	cs, err := NewClientset(good)
	if err != nil {
		t.Fatalf("NewClientset(valid kubeconfig): %v", err)
	}
	if cs == nil {
		t.Fatalf("NewClientset returned nil clientset with nil error")
	}
	if cs.CoreV1() == nil {
		t.Fatalf("clientset CoreV1 is nil")
	}
}

func TestNewClientset_PropagatesLoadError(t *testing.T) {
	notInCluster(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")

	cs, err := NewClientset(missing)
	if err == nil {
		t.Fatalf("NewClientset of a missing file returned nil error")
	}
	if cs != nil {
		t.Fatalf("NewClientset error path must return nil clientset, got non-nil")
	}
}
