package dgx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider is the inference backend seam (doc 20 P3). The harness treats the returned
// text as UNTRUSTED — it parses + gates it before anything is staged. Swapping the
// backend (Groq, a local OpenAI-compatible server, or a fresh impl) is a Provider
// change, never an architectural one.
type Provider interface {
	// Name identifies the backend (recorded in candidate lineage).
	Name() string
	// Complete sends the system + user prompt and returns the model's raw text.
	Complete(ctx context.Context, system, user string) (string, error)
}

// ChatProvider is an OpenAI-compatible chat-completions client (Groq is one config;
// a local llama.cpp/Ollama/vLLM server is another — same API, different base URL).
type ChatProvider struct {
	name        string
	baseURL     string
	apiKey      string
	model       string
	temperature float64
	httpc       *http.Client
}

const defaultGroqModel = "llama-3.3-70b-versatile"

// NewGroqProvider builds the default backend: Groq's OpenAI-compatible endpoint. An
// empty model uses a sensible default; override via the DGX_MODEL env / config.
func NewGroqProvider(apiKey, model string) *ChatProvider {
	if strings.TrimSpace(model) == "" {
		model = defaultGroqModel
	}
	return &ChatProvider{
		name: "groq", baseURL: "https://api.groq.com/openai/v1", apiKey: apiKey,
		model: model, temperature: 0.1, httpc: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewOpenAICompatibleProvider points the same client at any OpenAI-compatible base URL
// (a local model server, a different vendor). This is the "local models without
// rework" path: only configuration changes.
func NewOpenAICompatibleProvider(name, baseURL, apiKey, model string) *ChatProvider {
	if strings.TrimSpace(name) == "" {
		name = "openai-compatible"
	}
	return &ChatProvider{
		name: name, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey,
		model: model, temperature: 0.1, httpc: &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *ChatProvider) Name() string { return p.name }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Complete posts a chat-completions request and returns the assistant content. It
// requests JSON-object output (best-effort; most Groq/OpenAI models honour it) but the
// harness validates the JSON regardless.
func (p *ChatProvider) Complete(ctx context.Context, system, user string) (string, error) {
	reqBody := chatRequest{
		Model:          p.model,
		Temperature:    p.temperature,
		MaxTokens:      1024, // bound the completion side of the model's rate/token limit
		ResponseFormat: &respFormat{Type: "json_object"},
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("dgx %s: marshal request: %w", p.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("dgx %s: new request: %w", p.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("dgx %s: request: %w", p.name, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dgx %s: status %d: %s", p.name, resp.StatusCode, truncate(string(data), 240))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("dgx %s: decode response: %w", p.name, err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("dgx %s: no choices in response", p.name)
	}
	return cr.Choices[0].Message.Content, nil
}

// StaticProvider returns a fixed response — for hermetic tests of the whole harness
// (no network, no key).
type StaticProvider struct {
	name     string
	response string
	err      error
}

// NewStaticProvider returns a provider that always replies with response (or err).
func NewStaticProvider(name, response string) *StaticProvider {
	return &StaticProvider{name: name, response: response}
}

// WithError makes the static provider fail (testing the error path).
func (p *StaticProvider) WithError(err error) *StaticProvider { p.err = err; return p }

func (p *StaticProvider) Name() string { return p.name }

func (p *StaticProvider) Complete(context.Context, string, string) (string, error) {
	return p.response, p.err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
