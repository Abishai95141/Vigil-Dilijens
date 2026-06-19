package dgx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// Complete sends the system + user prompt and returns the model's raw text
	// (the Phase-1 single-shot path).
	Complete(ctx context.Context, system, user string) (string, error)
	// CompleteTools runs ONE assistant turn of a multi-turn tool-calling exchange
	// (doc 21 Phase 2): given the running message history + the read-only tool
	// schemas, it returns either text (the final answer) or a set of tool calls to
	// dispatch. A provider that cannot do tool-calling returns ErrToolsUnsupported so
	// the agent falls back to the Complete() single-shot path.
	CompleteTools(ctx context.Context, msgs []ChatTurn, tools []ToolSchema) (AssistantTurn, error)
}

// ErrToolsUnsupported signals that a provider cannot do tool-calling; the agent loop
// degrades gracefully to the single-shot Complete() path (doc 21 §2.3 robustness).
var ErrToolsUnsupported = errors.New("dgx: provider does not support tool-calling")

// ChatTurn is one message in a tool-calling conversation. Role ∈ {system,user,assistant,tool}.
// An assistant turn that requested tools carries ToolCalls; a tool-result turn carries
// ToolCallID + Content (the rendered tool output).
type ChatTurn struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolCall is one function call the model asked for (OpenAI shape: Arguments is raw JSON).
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// AssistantTurn is one assistant response: EITHER final text (Content) OR a set of tool
// calls (ToolCalls). Exactly one is non-empty.
type AssistantTurn struct {
	Content   string
	ToolCalls []ToolCall
}

// ChatProvider is an OpenAI-compatible chat-completions client (Groq is one config;
// a local llama.cpp/Ollama/vLLM server is another — same API, different base URL).
type ChatProvider struct {
	name        string
	baseURL     string
	apiKey      string
	model       string
	temperature float64
	maxTokens   int
	extraBody   map[string]any // provider-specific request fields merged into every call
	httpc       *http.Client
}

const defaultGroqModel = "llama-3.3-70b-versatile"

// defaultMaxTokens bounds the completion. 2048 (not 1024) so a REASONING model — whose
// hidden reasoning_tokens count against this budget — has room to both think AND emit the
// proposals JSON; a 1024 cap starves such a model and it returns empty content (observed
// live on deepseek-v4-flash). Override per provider via SetMaxTokens / DGX_MAX_TOKENS.
const defaultMaxTokens = 2048

// NewGroqProvider builds the default backend: Groq's OpenAI-compatible endpoint. An
// empty model uses a sensible default; override via the DGX_MODEL env / config.
func NewGroqProvider(apiKey, model string) *ChatProvider {
	if strings.TrimSpace(model) == "" {
		model = defaultGroqModel
	}
	return &ChatProvider{
		name: "groq", baseURL: "https://api.groq.com/openai/v1", apiKey: apiKey,
		model: model, temperature: 0.1, maxTokens: defaultMaxTokens, httpc: &http.Client{Timeout: 60 * time.Second},
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
		model: model, temperature: 0.1, maxTokens: defaultMaxTokens, httpc: &http.Client{Timeout: 60 * time.Second},
	}
}

// SetMaxTokens overrides the completion-token bound (DGX_MAX_TOKENS). A larger budget is
// needed for reasoning models (deepseek-reasoner, deepseek-v4-flash) whose reasoning_tokens
// count against it; a non-positive value is ignored (keeps the default).
func (p *ChatProvider) SetMaxTokens(n int) {
	if n > 0 {
		p.maxTokens = n
	}
}

// SetExtraBody merges provider-specific top-level fields into every request (DGX_EXTRA_BODY).
// Vendor-agnostic escape hatch: e.g. {"thinking":{"type":"disabled"}} turns OFF deepseek-v4-
// flash's reasoning (no reasoning_tokens — faster, cheaper, and more decisive tool-use), or
// {"reasoning_effort":"low"}. The fields are merged AFTER the typed request, so a vendor key
// the struct does not model still reaches the wire. Never set a string field on clock.proto —
// this is the LLM transport, entirely off the deterministic path.
func (p *ChatProvider) SetExtraBody(m map[string]any) {
	if len(m) > 0 {
		p.extraBody = m
	}
}

func (p *ChatProvider) Name() string { return p.name }

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireFunctionCall `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
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
	Tools          []wireTool    `json:"tools,omitempty"`
	ToolChoice     string        `json:"tool_choice,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// doChat posts one chat-completions request and decodes the response. Shared by Complete
// and CompleteTools so the auth/transport/error path is one source of truth. A non-200 is
// an error carrying the status (the "status 429" substring is preserved so the live loop's
// rate-limit backoff still matches, main.go isRateLimited).
func (p *ChatProvider) doChat(ctx context.Context, reqBody chatRequest) (*chatResponse, int, error) {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("dgx %s: marshal request: %w", p.name, err)
	}
	if len(p.extraBody) > 0 {
		if b, err = mergeExtraBody(b, p.extraBody); err != nil {
			return nil, 0, fmt.Errorf("dgx %s: merge extra body: %w", p.name, err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, 0, fmt.Errorf("dgx %s: new request: %w", p.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.httpc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("dgx %s: request: %w", p.name, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("dgx %s: status %d: %s", p.name, resp.StatusCode, truncate(string(data), 240))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("dgx %s: decode response: %w", p.name, err)
	}
	return &cr, resp.StatusCode, nil
}

// Complete posts a single-shot chat-completions request and returns the assistant content.
// It requests JSON-object output (best-effort) but the harness validates the JSON regardless.
func (p *ChatProvider) Complete(ctx context.Context, system, user string) (string, error) {
	cr, _, err := p.doChat(ctx, chatRequest{
		Model:          p.model,
		Temperature:    p.temperature,
		MaxTokens:      p.maxTokens,
		ResponseFormat: &respFormat{Type: "json_object"},
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("dgx %s: no choices in response", p.name)
	}
	return cr.Choices[0].Message.Content, nil
}

// CompleteTools runs one tool-calling turn. It attaches the tool schemas (tool_choice
// "auto") and posts the running history; a response carrying tool_calls becomes an
// AssistantTurn with ToolCalls, otherwise the assistant text. A 4xx whose body mentions
// tools/functions is mapped to ErrToolsUnsupported so the agent degrades to single-shot.
func (p *ChatProvider) CompleteTools(ctx context.Context, msgs []ChatTurn, tools []ToolSchema) (AssistantTurn, error) {
	req := chatRequest{
		Model:       p.model,
		Temperature: p.temperature,
		MaxTokens:   p.maxTokens,
		Messages:    toWireMessages(msgs),
	}
	if len(tools) > 0 {
		req.Tools = toWireTools(tools)
		req.ToolChoice = "auto"
	}
	cr, status, err := p.doChat(ctx, req)
	if err != nil {
		if (status == http.StatusBadRequest || status == http.StatusNotFound) &&
			(strings.Contains(strings.ToLower(err.Error()), "tool") || strings.Contains(strings.ToLower(err.Error()), "function")) {
			return AssistantTurn{}, ErrToolsUnsupported
		}
		return AssistantTurn{}, err
	}
	if len(cr.Choices) == 0 {
		return AssistantTurn{}, fmt.Errorf("dgx %s: no choices in response", p.name)
	}
	msg := cr.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		out := make([]ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			out = append(out, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
		return AssistantTurn{ToolCalls: out}, nil
	}
	// A 200 with neither tool calls nor text is a degenerate response (an overloaded/buggy
	// endpoint) — surface it as an explicit error rather than a confusing downstream parse
	// failure on empty content.
	if strings.TrimSpace(msg.Content) == "" {
		return AssistantTurn{}, fmt.Errorf("dgx %s: empty response (no tool calls, no text content)", p.name)
	}
	return AssistantTurn{Content: msg.Content}, nil
}

// mergeExtraBody splices provider-specific top-level fields into a marshaled request body.
// The extra fields win on a key clash (the operator's explicit override is authoritative).
func mergeExtraBody(base []byte, extra map[string]any) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		rv, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = rv
	}
	return json.Marshal(m)
}

func toWireMessages(turns []ChatTurn) []chatMessage {
	out := make([]chatMessage, len(turns))
	for i, t := range turns {
		m := chatMessage{Role: t.Role, Content: t.Content, ToolCallID: t.ToolCallID}
		for _, c := range t.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, wireToolCall{
				ID: c.ID, Type: "function",
				Function: wireFunctionCall{Name: c.Name, Arguments: c.Arguments},
			})
		}
		out[i] = m
	}
	return out
}

func toWireTools(tools []ToolSchema) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type:     "function",
			Function: wireToolFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}
	return out
}

// StaticProvider is the hermetic test backend (no network, no key). It can be a single
// fixed response (NewStaticProvider — the Phase-1 seam, unchanged) OR a SCRIPTED sequence
// of turns (NewScriptedProvider().AddToolCall(...).AddText(...)) so a test can drive the
// multi-turn tool loop: each CompleteTools call pops the next scripted turn.
type StaticProvider struct {
	name     string
	response string // Complete() returns this (single-shot seam)
	err      error
	turns    []scriptTurn // CompleteTools pops these in order
	idx      int
	toolErr  error // when set, CompleteTools returns it (e.g. ErrToolsUnsupported)
}

type scriptTurn struct {
	text  string
	calls []ToolCall
}

// NewStaticProvider returns a provider that always replies with response from Complete and
// a single text turn from CompleteTools (back-compat with every Phase-1 test).
func NewStaticProvider(name, response string) *StaticProvider {
	return &StaticProvider{name: name, response: response, turns: []scriptTurn{{text: response}}}
}

// NewScriptedProvider returns a provider whose CompleteTools replays scripted turns in
// order (tool-call turns then a final text turn). Complete returns the LAST scripted text.
func NewScriptedProvider(name string) *StaticProvider { return &StaticProvider{name: name} }

// AddToolCall appends a turn in which the model asks to call one tool.
func (p *StaticProvider) AddToolCall(id, name, args string) *StaticProvider {
	p.turns = append(p.turns, scriptTurn{calls: []ToolCall{{ID: id, Name: name, Arguments: args}}})
	return p
}

// AddText appends a final text turn (typically the proposals JSON). It also becomes the
// Complete() response so a fallback to single-shot yields the same text.
func (p *StaticProvider) AddText(text string) *StaticProvider {
	p.turns = append(p.turns, scriptTurn{text: text})
	p.response = text
	return p
}

// WithError makes Complete fail (testing the error path).
func (p *StaticProvider) WithError(err error) *StaticProvider { p.err = err; return p }

// WithToolsUnsupported makes CompleteTools return ErrToolsUnsupported (testing the
// graceful single-shot fallback). Complete still returns the configured response.
func (p *StaticProvider) WithToolsUnsupported() *StaticProvider {
	p.toolErr = ErrToolsUnsupported
	return p
}

func (p *StaticProvider) Name() string { return p.name }

func (p *StaticProvider) Complete(context.Context, string, string) (string, error) {
	return p.response, p.err
}

func (p *StaticProvider) CompleteTools(context.Context, []ChatTurn, []ToolSchema) (AssistantTurn, error) {
	if p.toolErr != nil {
		return AssistantTurn{}, p.toolErr
	}
	if p.err != nil {
		return AssistantTurn{}, p.err
	}
	if p.idx >= len(p.turns) {
		return AssistantTurn{Content: ""}, nil // exhausted: an empty final turn (the loop bounds itself)
	}
	t := p.turns[p.idx]
	p.idx++
	return AssistantTurn{Content: t.text, ToolCalls: t.calls}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
