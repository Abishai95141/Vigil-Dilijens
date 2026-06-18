//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This file is the e2e harness: it builds the shipped binaries, drives obsd against
// a live cluster, polls its HTTP API, and shells out to kubectl. It is deliberately
// black-box — it asserts over the SAME HTTP surface an operator uses, decoding into
// minimal local structs (in e2e_test.go) so the suite is robust to internal refactors.

// --- repo root + binary build (once per `go test` process) ------------------

var (
	repoRootOnce sync.Once
	repoRootDir  string
	repoRootErr  error

	buildOnce sync.Once
	obsdBin   string
	replayBin string
	buildErr  error
)

// repoRoot walks up from this source file to the module root (the dir with go.mod),
// so the suite runs regardless of the caller's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	repoRootOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			repoRootErr = fmt.Errorf("runtime.Caller failed")
			return
		}
		dir := filepath.Dir(file)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				repoRootDir = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				repoRootErr = fmt.Errorf("go.mod not found above %s", filepath.Dir(file))
				return
			}
			dir = parent
		}
	})
	if repoRootErr != nil {
		t.Fatalf("locate repo root: %v", repoRootErr)
	}
	return repoRootDir
}

// buildBinaries builds the SHIPPED obsd + replay (CGO-free, like CI) into a temp dir,
// once per process. The suite tests the real binaries, not an in-process stand-in.
func buildBinaries(t *testing.T) (obsd, replay string) {
	t.Helper()
	root := repoRoot(t)
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vigil-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		build := func(name, pkg string) string {
			out := filepath.Join(dir, name)
			cmd := exec.Command("go", "build", "-o", out, pkg)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if b, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v\n%s", name, err, b)
				return ""
			}
			return out
		}
		obsdBin = build("obsd", "./obsd/cmd/obsd")
		if buildErr == nil {
			replayBin = build("replay", "./obsd/cmd/replay")
		}
	})
	if buildErr != nil {
		t.Fatalf("build binaries: %v", buildErr)
	}
	return obsdBin, replayBin
}

// --- cluster prerequisites --------------------------------------------------

// kubeconfigPath resolves the kubeconfig: VIGIL_TEST_KUBECONFIG, then KUBECONFIG,
// then the default ~/.kube/config. Skips (never fails) when no cluster is reachable —
// an integration test with no cluster is genuinely not runnable, not a failure.
func kubeconfigPath(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("VIGIL_TEST_KUBECONFIG"); v != "" {
		return v
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(def); err == nil {
		return def
	}
	t.Skip("no kubeconfig (set VIGIL_TEST_KUBECONFIG or KUBECONFIG, or place ~/.kube/config)")
	return ""
}

// requireKubectl skips the suite when kubectl is absent (manifests are applied via it).
func requireKubectl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not on PATH")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// --- obsd process handle ----------------------------------------------------

type obsdProc struct {
	t       *testing.T
	baseURL string
	dataDir string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	logPath string
	done    chan struct{}
	exitErr error
	stopped bool
}

// startObsd builds and launches obsd against the cluster with --api + a private
// findings DB + replay capture, on a free localhost port, and waits for readiness.
// It is bound to 127.0.0.1 deliberately: the served API is unauthenticated today, so
// the suite never exposes it beyond loopback (the security track addresses auth).
func startObsd(t *testing.T, kc, dataDir string, extra ...string) *obsdProc {
	t.Helper()
	obsd, _ := buildBinaries(t)
	root := repoRoot(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	logPath := filepath.Join(dataDir, "obsd.log")
	logF, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create obsd log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := append([]string{
		"--kubeconfig", kc,
		"--health-addr", addr,
		"--db", filepath.Join(dataDir, "vigil.db"),
		"--store-dir", filepath.Join(dataDir, "captures"),
		"--api",
		"--log", "text",
	}, extra...)
	cmd := exec.CommandContext(ctx, obsd, args...)
	cmd.Dir = root // relative ontology/overlays/releases paths resolve from the repo root
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Graceful stop: ctx cancel -> SIGINT (obsd seals the replay bundle) -> grace -> kill.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	cmd.WaitDelay = 15 * time.Second

	if err := cmd.Start(); err != nil {
		cancel()
		logF.Close()
		t.Fatalf("start obsd: %v", err)
	}
	p := &obsdProc{t: t, baseURL: "http://" + addr, dataDir: dataDir, cmd: cmd, cancel: cancel, logPath: logPath, done: make(chan struct{})}
	go func() {
		p.exitErr = cmd.Wait()
		logF.Close()
		close(p.done)
	}()

	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			p.dumpLog()
		}
	})
	p.waitReady(2 * time.Minute)
	return p
}

// stop signals obsd to shut down and waits for it to seal + exit.
func (p *obsdProc) stop() {
	if p.stopped {
		return
	}
	p.stopped = true
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(25 * time.Second):
		p.t.Logf("obsd did not exit within 25s of stop")
	}
}

// waitReady blocks until /readyz is 200 (informers synced) or obsd exits early.
func (p *obsdProc) waitReady(timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			p.dumpLog()
			p.t.Fatalf("obsd exited before becoming ready: %v", p.exitErr)
		default:
		}
		resp, err := http.Get(p.baseURL + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				p.t.Logf("obsd ready at %s", p.baseURL)
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	p.dumpLog()
	p.t.Fatalf("obsd not ready after %s (log: %s)", timeout, p.logPath)
}

// getJSON GETs an API path and decodes the body into dst.
func (p *obsdProc) getJSON(path string, dst any) error {
	resp, err := http.Get(p.baseURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, bytes.TrimSpace(b))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// rssKiB reads obsd's resident set size from /proc (the Linux test/cluster hosts),
// in KiB — the live memory footprint at the current pod count.
func (p *obsdProc) rssKiB() (int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", p.cmd.Process.Pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line) // "VmRSS:  123456 kB"
			if len(f) >= 2 {
				return strconv.Atoi(f[1])
			}
		}
	}
	return 0, fmt.Errorf("VmRSS not found in /proc/%d/status", p.cmd.Process.Pid)
}

// metricValue scrapes /metrics and returns the value of the first sample whose metric
// name (ignoring labels) ends with nameSuffix — e.g. "_misjoins". The metric namespace
// prefix varies, so match by suffix.
func (p *obsdProc) metricValue(nameSuffix string) (float64, bool) {
	resp, err := http.Get(p.baseURL + "/metrics")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		if strings.HasSuffix(name, nameSuffix) {
			if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

// dumpLog prints the tail of obsd's log (for diagnosing a failed assertion).
func (p *obsdProc) dumpLog() {
	b, err := os.ReadFile(p.logPath)
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	const tail = 140
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	p.t.Logf("--- obsd log tail (%s) ---\n%s\n--- end obsd log ---", p.logPath, bytes.Join(lines, []byte("\n")))
}

// --- polling + kubectl helpers ----------------------------------------------

// eventually polls fn until it returns nil or the timeout elapses, then fails with
// the last error. The single assertion idiom for "the live pipeline reaches a state".
func eventually(t *testing.T, timeout, interval time.Duration, desc string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for {
		if last = fn(); last == nil {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out after %s waiting for %s: %v", timeout, desc, last)
		}
		time.Sleep(interval)
	}
}

func kubectlOut(kc string, args ...string) ([]byte, error) {
	full := append([]string{"--kubeconfig", kc}, args...)
	return exec.Command("kubectl", full...).CombinedOutput()
}

func kubectl(t *testing.T, kc string, args ...string) string {
	t.Helper()
	out, err := kubectlOut(kc, args...)
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// applyChaos applies a manifest under corpus/chaos/e2e/ (path relative to repo root),
// retrying transient failures — most importantly a namespace still draining from a
// prior run's --wait=false delete ("namespace is being terminated"), which resolves
// once the old namespace finishes terminating and the manifest recreates it.
func applyChaos(t *testing.T, kc, relPath string) {
	t.Helper()
	path := filepath.Join(repoRoot(t), relPath)
	deadline := time.Now().Add(60 * time.Second)
	var lastOut []byte
	var lastErr error
	for {
		out, err := kubectlOut(kc, "apply", "-f", path)
		if err == nil {
			return
		}
		lastOut, lastErr = out, err
		if !time.Now().Before(deadline) {
			t.Fatalf("kubectl apply -f %s failed after retries: %v\n%s", relPath, lastErr, lastOut)
		}
		time.Sleep(3 * time.Second)
	}
}

// deleteNamespace tears down a namespace, best-effort and non-blocking.
func deleteNamespace(kc, ns string) {
	_, _ = kubectlOut(kc, "delete", "namespace", ns, "--ignore-not-found", "--wait=false")
}

// unstickNamespace force-finalizes a namespace wedged in Terminating — the symptom of
// a degraded aggregation layer (e.g. a metrics.k8s.io APIService with MissingEndpoints
// makes the namespace controller's discovery step fail, so GC never completes; observed
// live on a k3s box whose metrics-server had been down 16 days). By then the namespace
// is already empty, so clearing the residual finalizer via the /finalize subresource
// just lets the delete finish. A NO-OP on a healthy cluster (namespaces finalize in
// seconds) and when the namespace is absent — it only ever acts where it's needed.
func unstickNamespace(t *testing.T, kc, ns string) {
	t.Helper()
	out, err := kubectlOut(kc, "get", "namespace", ns, "-o", "json")
	if err != nil {
		return // absent
	}
	var obj map[string]any
	if json.Unmarshal(out, &obj) != nil {
		return
	}
	if status, _ := obj["status"].(map[string]any); status == nil || status["phase"] != "Terminating" {
		return // Active or unknown — leave it; a normal delete handles Active.
	}
	if spec, ok := obj["spec"].(map[string]any); ok {
		spec["finalizers"] = []any{}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return
	}
	tmp := filepath.Join(t.TempDir(), "ns-finalize-"+ns+".json")
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	if o, err := kubectlOut(kc, "replace", "--raw", "/api/v1/namespaces/"+ns+"/finalize", "-f", tmp); err != nil {
		t.Logf("force-finalize %s: %v\n%s", ns, err, o)
	} else {
		t.Logf("force-finalized stuck namespace %s (degraded aggregation layer — see the metrics.k8s.io APIService)", ns)
	}
}

// prepareNamespace ensures ns is absent before the suite recreates it: it clears a
// prior run's wedged delete (unstickNamespace) and waits for the namespace to be gone,
// so the chaos apply isn't rejected with "namespace is being terminated".
func prepareNamespace(t *testing.T, kc, ns string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := kubectlOut(kc, "get", "namespace", ns); err != nil {
			return // absent -> ready to recreate
		}
		unstickNamespace(t, kc, ns)
		if !time.Now().Before(deadline) {
			t.Logf("namespace %s still present after 90s; proceeding (apply retries as a backstop)", ns)
			return
		}
		time.Sleep(2 * time.Second)
	}
}
