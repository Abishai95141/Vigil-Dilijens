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
	// --- the synthesis relay (v3.1): the full classed picture, so an MCP-connected
	// AI can SYNTHESIZE a cause + a suggested remediation from grounded facts rather
	// than guess. The charter discipline moves from input-withholding to output-
	// labeling: every payload below is already provenance-classed; the AI's synthesis
	// is disciplined at the boundary (cite, label-its-own-inference, validate_claim).
	// All are read-only snapshot funcs (no writer in scope ⇒ no-write-back STRUCTURAL).
	Insights       func() *api.InsightsView       // MEASURED ⋈ AUTHORED — evidence trail + authored why
	RootCauseChain func() *api.RootCauseChainView // MEASURED ⋈ AUTHORED — the transitive chain (the cause spine)
	CrossService   func() *api.CrossServiceView   // MEASURED ⋈ AUTHORED — the one-hop cascade
	Topology       func() *api.TopologyView       // MEASURED — the bound dependency graph
	Unexplained    func() *api.UnexplainedView    // MEASURED — loud-but-unmatched + the stated blind spot
	Departures     func() *api.DepartureView      // PROJECTED — band-departure anomalies
	// AuthoredRelations is the curated causal map (AUTHORED) — the only legitimate
	// causal basis, so the agent can tell an authored cause from a co-occurrence.
	AuthoredRelations func() *api.AuthoredRelationsView
	// Blindspots is the registry of what Vigil CANNOT see (doc 19) — modality-absent
	// phenomena, the charter's epistemic floors, and this cluster's unobtainable
	// signals — so the agent tells an authored cause from an unobservable modality
	// before committing, instead of over-claiming a cause Vigil never had a sensor for.
	Blindspots func() *api.BlindspotRegistryView
	// Referee validates an external claim against the charter + authored graph
	// (v3 T-D). Advisory — NEVER blocks. nil ⇒ the validate_claim tool reports off.
	Referee func(claim string) api.ClaimVerdict
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
			"instructions":    synthesisInstructions,
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
	// the synthesis relay (v3.1)
	toolInsights       = "get_insights"
	toolRootCauseChain = "get_root_cause_chain"
	toolCrossService   = "get_cross_service"
	toolTopology       = "get_topology"
	toolUnexplained    = "get_unexplained"
	toolDepartures     = "get_departures"
	toolAuthoredRels   = "get_authored_relations"
	toolBlindspots     = "get_blindspots"

	toolValidateClaim = "validate_claim"
	toolEmitAdvisory  = "emit_advisory"
)

// synthesisInstructions is the server-level guide handed to the model at
// `initialize`. It frames Vigil as the classed-fact substrate and the AI as the
// synthesizer, with the discipline that keeps the separation honest.
const synthesisInstructions = "Vigil is a read-only, deterministic observability substrate for AI synthesis. " +
	"Every tool returns ALREADY-CLASSED facts: MEASURED (read from the store or an arithmetic consequence), " +
	"PROJECTED (a forecast band — never a single line, never the word \"will\"), AUTHORED (a curated graph note, " +
	"surfaced verbatim with author+version). YOUR job is to SYNTHESIZE the cause and a suggested remediation FROM " +
	"these facts — Vigil never authors prose or fixes. The separation is preserved at the OUTPUT, not by starving you: " +
	"(1) ground every claim in a tool result and cite it; (2) get_root_cause_chain + get_cross_service ARE Vigil's " +
	"already-computed cause — relay/narrate them, never derive a different one; (3) get_topology is structure (raw " +
	"adjacency), not causation; (4) anything you infer BEYOND an authored relation (get_authored_relations is the only " +
	"legitimate causal basis) is YOUR hypothesis — label it so, never as a Vigil fact; (5) run every causal/forecast " +
	"sentence through validate_claim, then emit via emit_advisory. " +
	"Incident playbook: get_root_cause_chain (the spine) -> get_insights (evidence + the authored why) -> " +
	"get_cross_service + get_topology (blast radius) -> get_warnings (what crosses a bar soon + the lead time) -> " +
	"get_incidents (has it recurred / how often) -> get_events (discrete failures) -> get_unexplained + " +
	"get_silence_ledger + get_blindspots (the honest blind spots — what is NOT watched and what Vigil structurally " +
	"CANNOT see; if a degraded workload has no finding explaining it, the cause is likely a blind spot here — say so " +
	"and recommend an out-of-band check, never blame a visible-but-unflagged object) -> draft -> validate_claim -> " +
	"emit_advisory. The remediation is YOURS to reason from ops knowledge + application context you gather; Vigil " +
	"supplies only the grounded, classed facts."

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
		// --- the synthesis relay (v3.1): the full classed picture for cause synthesis ---
		{
			Name:        toolRootCauseChain,
			Description: "MEASURED ⋈ AUTHORED — the SPINE of a cause; start here for an incident. The transitive root-cause chain: MEASURED-degraded workloads stitched into an ORDERED chain over observed-flow edges, oriented ONLY by an authored relation, with the deepest degraded callee marked ROOT and the orientation “why” quoted verbatim (author+version). Silent intermediates are stated GAPS, never bridged. This chain IS Vigil's already-computed cause — relay/narrate it; never derive a different root. Also carries the gate-pending PROJECTED multi-hop ripple, clearly marked (do not present it as measured).",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolInsights,
			Description: "MEASURED ⋈ AUTHORED — the why-feed and your primary GROUNDING. Every phenomenon matched right now, each with its MEASURED evidence trail (the member signals that crossed, their config-sourced bar + state) AND the AUTHORED member note — the only curated “why”, attributed verbatim. Also carries recognized cascades (co-occurrence ⋈ authored relation). Use it to ground WHAT is degraded and to QUOTE the authored why; never invent a why the note does not state.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolCrossService,
			Description: "MEASURED ⋈ AUTHORED — the one-hop cascade: a degraded callee reaching its impacted callers over an observed-flow edge, impact traveling AGAINST the call arrow (callee → caller), oriented by the authored relation. A narrower, higher-confidence view than the transitive chain; use it to confirm the immediate blast direction.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolTopology,
			Description: "MEASURED — the bound cluster graph: workloads, nodes, services and their edges (observed call/flow, runs-on, service-routing, storage), each node carrying its current health mark. Use it to reason about DEPENDENCIES and a fix's blast radius, and to GROUND a cause in the chain — NOT to infer a cause the chain does not show (raw adjacency is not causation).",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolUnexplained,
			Description: "MEASURED — the loud-but-unmatched channel: signals anomalous against a resolved bar that match NO known phenomenon, plus candidate patterns proposed for human curation, plus the channel's OWN stated blind spot. Read as “there is more here than the known patterns explain” — investigate, do not alarm. These are co-occurrences, never causes.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolDepartures,
			Description: "PROJECTED — band-departure anomalies: a MEASURED sample that left its OWN recent forecast band beyond a structural margin (the band IS the bar; never learned). Gate-pending until a live capture flips the gate — it states that honestly. Read as an early, uncertain “this series is behaving outside its own forecast”, never as a measured fact.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolAuthoredRels,
			Description: "AUTHORED — the curated causal map: every directed phenomenon→phenomenon relation Vigil's ontology authors (Src→Dst + the verbatim why), plus the phenomenon vocabulary (id → human aliases). This is the ONLY legitimate causal basis: an observed co-occurrence is an authored CAUSE only if a relation for it appears here; otherwise it is coincidence (say so). Use it to tell authored causes from co-occurrence and to map ids in a chain/finding to meaning. ~13 relations — Vigil never invents a 14th.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolBlindspots,
			Description: "MEASURED (about own coverage). The blindspot registry — what Vigil CANNOT see. STATIC entries: modality-absent phenomena (no DNS/cert/etcd series), un-wired phenomena, missing modalities (no application-log lane, no app/DB-internal metrics, no L7 semantics), and the charter's EPISTEMIC FLOORS (e.g. Vigil sees a container is throttled, NOT which process inside it consumes the resource; a co-occurrence is never proof of cause). DYNAMIC entries: this cluster's signals that are unobtainable/un-ingested, with verbatim reasons. CONSULT THIS BEFORE attributing a cause: if a workload is degraded but no Vigil finding explains it, the cause is likely something here — say so and recommend an out-of-band check, never substitute a visible-but-unflagged object as the culprit. It only states absence; it never claims a phenomenon is occurring.",
			InputSchema: emptyObjectSchema,
		},
		{
			Name:        toolValidateClaim,
			Description: "The HONEST-LABELER for your synthesis — it NEVER blocks; a flag is a labeling instruction, not a veto. Submit a drafted causal/forecast clause; it checks it against the charter + the AUTHORED graph and returns {flagged, reasons[{class}], matchedAuthored}. Use it to LABEL, not delete: matchedAuthored=true ⇒ an authored relation backs this, present it AS authored (quote it); flagged generated-causation ⇒ no authored relation backs it, keep it but label it YOUR hypothesis (not a Vigil fact); flagged future-certainty ⇒ you stated a projection as certain, soften to a band; flagged class-fusion ⇒ you called a PROJECTED subject MEASURED, separate the classes. Run every causal/forecast sentence through this before emit_advisory.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"claim":{"type":"string","description":"the drafted claim to referee"}},"required":["claim"],"additionalProperties":false}`),
		},
		{
			Name:        toolEmitAdvisory,
			Description: "Emit your SYNTHESIZED narrative (cause and/or suggested remediation) as the ADVISORY class — clearly YOUR synthesis, distinct from Vigil's MEASURED/PROJECTED/AUTHORED facts and grounded in citations to the read tools. The text is charter-checked: a draft asserting an UN-authored cause as fact, or a future certainty, is REFUSED (re-draft using validate_claim's labels); a clean draft is WITHHELD until the advisory gate passes. ADVISORY is never written back into Vigil.",
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
	case toolRootCauseChain:
		return toolJSON(orNil(s.src.RootCauseChain), "root-cause chain not available (flow discovery off)"), nil
	case toolInsights:
		return toolJSON(orNil(s.src.Insights), "insights surface not available (api off)"), nil
	case toolCrossService:
		return toolJSON(orNil(s.src.CrossService), "cross-service surface not available (flow discovery off)"), nil
	case toolTopology:
		return toolJSON(orNil(s.src.Topology), "topology surface not available (api off)"), nil
	case toolUnexplained:
		return toolJSON(orNil(s.src.Unexplained), "unexplained channel not available (api off)"), nil
	case toolDepartures:
		return toolJSON(orNil(s.src.Departures), "departures surface not available (api off)"), nil
	case toolAuthoredRels:
		return toolJSON(orNil(s.src.AuthoredRelations), "authored-relations map not available (graph not loaded)"), nil
	case toolBlindspots:
		return toolJSON(orNil(s.src.Blindspots), "blindspot registry not available (api off)"), nil
	case toolValidateClaim:
		if s.src.Referee == nil {
			return mustRaw(toolResult{Content: []toolContent{{Type: "text", Text: `{"available":false,"note":"the validate-claim referee is not enabled (needs --referee-enabled)"}`}}}), nil
		}
		var args struct {
			Claim string `json:"claim"`
		}
		if len(p.Arguments) > 0 {
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return nil, &rpcErr{Code: codeInvalidParams, Message: "invalid validate_claim arguments: " + err.Error()}
			}
		}
		if args.Claim == "" {
			return nil, &rpcErr{Code: codeInvalidParams, Message: "validate_claim requires a non-empty claim"}
		}
		return mustRaw(textResult(s.src.Referee(args.Claim))), nil
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
