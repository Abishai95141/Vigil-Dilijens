//go:build integration

package clock

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cross-language swap test (doc 09 M1 exit): the REAL Go client speaks to
// the REAL Python clockd over loopback gRPC, running the SAME conformance
// fixtures both language-local suites use. The clock kind is a flag on the
// server — proving a swap touches zero knowledge on this side of the wire.
//
// Behind the integration tag (subprocess + loopback network). Skips when uv
// is unavailable. VIGIL_CLOCK_KIND=timesfm runs the same test against the
// pinned model (heavy: needs the `model` extra + cached checkpoint).
func TestSwapAgainstPythonClockd(t *testing.T) {
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not on PATH — clockd swap test needs the Python toolchain")
	}
	kind := os.Getenv("VIGIL_CLOCK_KIND")
	if kind == "" {
		kind = "stub"
	}
	extra := "serve"
	if kind == "timesfm" {
		extra = "model"
	}

	port := freePort(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(uv, "run", "--extra", extra,
		"python", "-m", "clockd.server", "--port", fmt.Sprint(port), "--clock", kind)
	cmd.Dir = filepath.Join(repoRoot, "clockd")
	// uv re-execs python as a grandchild; killing uv alone leaks the server and
	// leaves the stdout pipe open (the test binary then stalls on I/O). Own a
	// process GROUP and tear the whole tree down.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()

	// Wait for the serving line (model load can take a few seconds).
	readyCh := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "clockd serving") {
				close(readyCh)
				return
			}
		}
	}()
	select {
	case <-readyCh:
	case <-time.After(120 * time.Second):
		t.Fatal("clockd did not report serving within 120s")
	}

	c, err := New(fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rctx, rcancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer rcancel()
	ready, code, err := c.Health(rctx)
	if err != nil || !ready || code != 0 {
		t.Fatalf("health over the wire: ready=%v code=%d err=%v", ready, code, err)
	}

	doc := loadConformance(t)
	for _, cs := range doc.Cases {
		fc, err := c.Forecast(rctx, cs.Series, doc.Horizon, doc.Quantiles)
		if err != nil {
			t.Fatalf("%s (clock=%s): %v", cs.Name, kind, err)
		}
		assertContract(t, cs.Name, fc, doc, cs.Series, cs.Checks)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
