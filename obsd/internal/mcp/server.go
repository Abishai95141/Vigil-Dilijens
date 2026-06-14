package mcp

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
)

// protocolVersion is the MCP revision this adapter speaks.
const protocolVersion = "2024-11-05"

// maxRequestBody caps an inbound JSON-RPC frame (the MCP transport carries small
// operator/LLM requests; a large body is rejected, never buffered).
const maxRequestBody = 64 * 1024

// Sources is the read-only slice of api.Providers the MCP server exposes. Defining
// it as a struct of reader funcs (rather than the whole api.Providers) keeps
// no-write-back STRUCTURAL — there is no setter, writer, or mutable handle in scope.
// Each func returns an already-classed, immutable snapshot (or nil when its lane is
// off, which the server surfaces honestly).
type Sources struct {
	Coverage      func() *api.CoverageView
	SilenceLedger func() *api.SilenceLedgerView
	Warnings      func() *api.WarningsView
	Incidents     func() *api.IncidentsView
	Events        func() *api.EventsView
}

// Server is a READ-ONLY MCP adapter. See doc.go for the charter contract.
type Server struct {
	src                Sources
	advisoryGatePassed bool
	serverName         string
	serverVersion      string
}

// New constructs the adapter. advisoryGatePassed=false withholds ADVISORY content
// (the class still refuses banned drafts) until the gate passes.
func New(src Sources, advisoryGatePassed bool, name, version string) *Server {
	if name == "" {
		name = "vigil-obsd"
	}
	return &Server{src: src, advisoryGatePassed: advisoryGatePassed, serverName: name, serverVersion: version}
}

// --- JSON-RPC 2.0 envelopes ------------------------------------------------

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Handle processes one JSON-RPC frame. It returns the response bytes and whether the
// frame was a notification (no id ⇒ no response is sent). It is a pure function of
// the frame plus the current snapshots — it mutates nothing.
func (s *Server) Handle(raw []byte) (out []byte, isNotification bool) {
	var req rpcReq
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.errorResp(nil, codeParse, "parse error: "+err.Error()), false
	}
	if req.JSONRPC != "2.0" {
		return s.errorResp(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\""), false
	}
	notification := len(req.ID) == 0

	result, rerr := s.dispatch(req.Method, req.Params)
	if notification {
		// Notifications (e.g. notifications/initialized) get no reply, by spec.
		return nil, true
	}
	if rerr != nil {
		return s.errorRespFrom(req.ID, rerr), false
	}
	resp := rpcResp{JSONRPC: "2.0", ID: req.ID, Result: result}
	b, _ := json.Marshal(resp)
	return b, false
}

func (s *Server) dispatch(method string, params json.RawMessage) (json.RawMessage, *rpcErr) {
	switch method {
	case "initialize":
		return mustRaw(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.serverName, "version": s.serverVersion},
			"instructions":    "Vigil read-only observability harness. Tools return already-classed facts (MEASURED/PROJECTED) verbatim — never restate a projection as a measurement or assert a cause. Lead with get_silence_ledger for a provable account of what is NOT watched and why.",
		}), nil
	case "ping":
		return mustRaw(map[string]any{}), nil
	case "tools/list":
		return mustRaw(map[string]any{"tools": toolDefs()}), nil
	case "tools/call":
		return s.callTool(params)
	default:
		return nil, &rpcErr{Code: codeMethodNotFound, Message: "method not found: " + method}
	}
}

// --- tools -----------------------------------------------------------------

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

const (
	toolCoverage      = "get_coverage"
	toolSilenceLedger = "get_silence_ledger"
	toolWarnings      = "get_warnings"
	toolIncidents     = "get_incidents"
	toolEvents        = "get_events"
	toolEmitAdvisory  = "emit_advisory"
)

var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

func toolDefs() []toolDef {
	return []toolDef{
		{
			Name:        toolCoverage,
			Description: "MEASURED (about own coverage). The Coverage Report: per-phenomenon observability, per-rule binding coverage, the unbounded list, and QA status. Every (entity,variable) pair has a visible state; gaps are stated, never blank.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolSilenceLedger,
			Description: "MEASURED (about own coverage). The deterministic ABSENCE ledger: every (entity,variable) pair Vigil produced is WATCHED or SILENT with an exact reason (unbounded / no-stream-key / unresolved / out-of-scope). Use this to state a provable negative — what is NOT being watched and why — instead of guessing.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolWarnings,
			Description: "PROJECTED. Early-warning forecast cards, each with a mandatory uncertainty band (earliest/latest, open when the far edge is beyond the horizon) and an isProjection mark. A projection is a band, never a certainty; never restate one as a measurement.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolIncidents,
			Description: "MEASURED. The durable cross-run incident memory: each phenomenon joined across time on its role, with how many distinct episodes (recurrence) it has had and over what span. Use this for 'has this happened before / how often'. Recurrence is a count, never a cause or a forecast.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolEvents,
			Description: "MEASURED. The discrete-event lane: k8s Events (OOMKilled, CrashLoopBackOff) ingested as findings and JOINED by shared role CEI to gauge phenomena (corroborate, never fuse). A corroboration carries an AUTHORED why verbatim; a standalone event is visible but never upgraded to a match. An event is a co-occurrence, never a cause.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolEmitAdvisory,
			Description: "Submit a generated operator-facing summary as the ADVISORY class. The text is charter-checked: a draft asserting a cause or a future certainty is REFUSED; a register-clean draft is WITHHELD until the advisory gate passes. ADVISORY is never MEASURED/PROJECTED/AUTHORED and is never written back into Vigil.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"the advisory prose to validate"}},"required":["text"],"additionalProperties":false}`),
		},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

func (s *Server) callTool(params json.RawMessage) (json.RawMessage, *rpcErr) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcErr{Code: codeInvalidParams, Message: "invalid tool-call params: " + err.Error()}
	}
	switch p.Name {
	case toolCoverage:
		return toolJSON(orNil(s.src.Coverage), "coverage lane not running"), nil
	case toolSilenceLedger:
		return toolJSON(orNil(s.src.SilenceLedger), "silence ledger not running"), nil
	case toolWarnings:
		return toolJSON(orNil(s.src.Warnings), "early-warning lane not running (off pending its gate)"), nil
	case toolIncidents:
		return toolJSON(orNil(s.src.Incidents), "incident memory not enabled (needs --incident-memory + --db)"), nil
	case toolEvents:
		return toolJSON(orNil(s.src.Events), "events lane not enabled (needs --events-enabled)"), nil
	case toolEmitAdvisory:
		var args struct {
			Text string `json:"text"`
		}
		if len(p.Arguments) > 0 {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return nil, &rpcErr{Code: codeInvalidParams, Message: "invalid emit_advisory arguments: " + err.Error()}
			}
		}
		return mustRaw(textResult(s.emitAdvisory(args.Text))), nil
	default:
		return nil, &rpcErr{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}
}

// orNil normalizes a possibly-nil source func into a value-or-nil.
func orNil[T any](f func() *T) *T {
	if f == nil {
		return nil
	}
	return f()
}

// toolJSON renders a view as a text tool result; a nil view becomes an honest
// "lane off" text result (isError=false: an off lane is a true state, not an error).
func toolJSON[T any](v *T, offMsg string) json.RawMessage {
	if v == nil {
		return mustRaw(toolResult{Content: []toolContent{{Type: "text", Text: `{"available":false,"note":"` + offMsg + `"}`}}})
	}
	b, err := json.Marshal(v)
	if err != nil {
		return mustRaw(toolResult{IsError: true, Content: []toolContent{{Type: "text", Text: "encoding error"}}})
	}
	return mustRaw(toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}})
}

func textResult(v any) toolResult {
	b, _ := json.Marshal(v)
	return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}
}

// --- HTTP transport --------------------------------------------------------

// HTTPHandler mounts the adapter as a single JSON-RPC-over-HTTP POST endpoint. A
// notification (no id) returns 204. The body is capped. (stdio / Streamable-HTTP
// transports can reuse Handle later; the dispatcher is transport-agnostic.)
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		out, isNotification := s.Handle(body)
		if isNotification {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(out)
	})
}

// --- helpers ---------------------------------------------------------------

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func (s *Server) errorResp(id json.RawMessage, code int, msg string) []byte {
	return s.errorRespFrom(id, &rpcErr{Code: code, Message: msg})
}

func (s *Server) errorRespFrom(id json.RawMessage, e *rpcErr) []byte {
	b, _ := json.Marshal(rpcResp{JSONRPC: "2.0", ID: id, Error: e})
	return b
}
