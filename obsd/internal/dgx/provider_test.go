package dgx_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// The OpenAI-compatible client builds the right request (path, auth, model, roles) and
// parses the assistant content — verified against a local fake endpoint, no real key.
func TestChatProviderRequestAndParse(t *testing.T) {
	var gotAuth, gotPath, gotBody, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
	}))
	defer srv.Close()

	p := dgx.NewOpenAICompatibleProvider("test", srv.URL, "secret-key", "test-model")
	out, err := p.Complete(context.Background(), "the system rules", "the user context")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"proposals":[]}` {
		t.Errorf("parsed content = %q", out)
	}
	if gotMethod != http.MethodPost || gotPath != "/chat/completions" {
		t.Errorf("request = %s %s, want POST /chat/completions", gotMethod, gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header = %q, want Bearer secret-key", gotAuth)
	}
	for _, want := range []string{`"model":"test-model"`, `"role":"system"`, `"role":"user"`, "the system rules"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body missing %q\nbody: %s", want, gotBody)
		}
	}
}

func TestChatProviderStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "invalid api key")
	}))
	defer srv.Close()
	p := dgx.NewOpenAICompatibleProvider("test", srv.URL, "bad", "m")
	if _, err := p.Complete(context.Background(), "s", "u"); err == nil {
		t.Error("a non-200 status must be an error")
	}
}

func TestGroqProviderName(t *testing.T) {
	if got := dgx.NewGroqProvider("k", "").Name(); got != "groq" {
		t.Errorf("groq provider name = %q", got)
	}
}

func TestStaticProvider(t *testing.T) {
	p := dgx.NewStaticProvider("s", "hello")
	out, err := p.Complete(context.Background(), "", "")
	if err != nil || out != "hello" {
		t.Errorf("static provider = %q, %v", out, err)
	}
}
