package dgx_test

import (
	"context"
	"encoding/json"
	"errors"
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

// CompleteTools sends the tool schemas + tool_choice, and parses BOTH a tool_calls response
// (→ ToolCalls) and a content response (→ Content) from the same endpoint.
func TestChatProviderCompleteTools(t *testing.T) {
	var gotBody string
	mode := "tools" // first call returns tool_calls, second returns content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		if mode == "tools" {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_strays","arguments":"{}"}}]}}]}`)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"proposals\":[]}"}}]}`)
		}
	}))
	defer srv.Close()
	p := dgx.NewOpenAICompatibleProvider("test", srv.URL, "k", "m")

	turn, err := p.CompleteTools(context.Background(),
		[]dgx.ChatTurn{{Role: "system", Content: "rules"}, {Role: "user", Content: "ctx"}},
		[]dgx.ToolSchema{{Name: "get_strays", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "get_strays" || turn.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %+v, want one get_strays/call_1", turn.ToolCalls)
	}
	for _, want := range []string{`"tools":`, `"tool_choice":"auto"`, `"name":"get_strays"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body missing %q\nbody: %s", want, gotBody)
		}
	}

	mode = "content"
	turn2, err := p.CompleteTools(context.Background(),
		[]dgx.ChatTurn{{Role: "assistant", ToolCalls: turn.ToolCalls}, {Role: "tool", ToolCallID: "call_1", Content: "- [stray:x] (stray-metric) x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn2.Content != `{"proposals":[]}` || len(turn2.ToolCalls) != 0 {
		t.Errorf("content turn = %+v", turn2)
	}
}

// A 4xx whose body mentions tools maps to ErrToolsUnsupported (the graceful-fallback signal).
func TestChatProviderToolsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"this model does not support tool use"}`)
	}))
	defer srv.Close()
	p := dgx.NewOpenAICompatibleProvider("test", srv.URL, "k", "m")
	_, err := p.CompleteTools(context.Background(), []dgx.ChatTurn{{Role: "user", Content: "x"}},
		[]dgx.ToolSchema{{Name: "t"}})
	if !errors.Is(err, dgx.ErrToolsUnsupported) {
		t.Fatalf("want ErrToolsUnsupported, got %v", err)
	}
}

// A 200 with neither tool_calls nor text content is an explicit error, not a confusing
// downstream parse failure on empty content.
func TestChatProviderEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	}))
	defer srv.Close()
	p := dgx.NewOpenAICompatibleProvider("test", srv.URL, "k", "m")
	_, err := p.CompleteTools(context.Background(), []dgx.ChatTurn{{Role: "user", Content: "x"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("empty 200 must be an explicit error, got %v", err)
	}
}

// The scripted StaticProvider replays tool-call then text turns in order; Complete still
// returns the final text (back-compat with the single-shot path).
func TestScriptedProvider(t *testing.T) {
	p := dgx.NewScriptedProvider("s").
		AddToolCall("c1", "get_strays", "{}").
		AddText(`{"proposals":[]}`)
	t1, _ := p.CompleteTools(context.Background(), nil, nil)
	if len(t1.ToolCalls) != 1 || t1.ToolCalls[0].Name != "get_strays" {
		t.Fatalf("turn 1 = %+v, want a get_strays call", t1)
	}
	t2, _ := p.CompleteTools(context.Background(), nil, nil)
	if t2.Content != `{"proposals":[]}` {
		t.Fatalf("turn 2 = %+v, want the proposals text", t2)
	}
	if out, _ := p.Complete(context.Background(), "", ""); out != `{"proposals":[]}` {
		t.Errorf("Complete fallback = %q", out)
	}
	// WithToolsUnsupported forces the fallback signal.
	pf := dgx.NewStaticProvider("s", "x").WithToolsUnsupported()
	if _, err := pf.CompleteTools(context.Background(), nil, nil); !errors.Is(err, dgx.ErrToolsUnsupported) {
		t.Errorf("WithToolsUnsupported should return ErrToolsUnsupported, got %v", err)
	}
}
