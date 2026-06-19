package dgx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Read-only retrieval tools (doc 21 Phase 2). The agent gathers MEASURED evidence on
// demand instead of reasoning over one fixed dump. Every tool is READ-ONLY: it returns
// Observations (each with a STABLE ref the agent may then cite) and NEVER writes. The
// Tool CONTRACT lives here in dgx; the implementations that touch the api views / stores
// live in package main (cmd/obsd/dgxtools.go) so the import firewall holds — no
// deterministic package, and not dgx, imports internal/api.

// ToolSchema is the OpenAI function schema advertised to the model for one tool.
type ToolSchema struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Tool is one read-only retrieval tool. Call returns a deterministic set of Observations
// (sorted+capped by the implementation); each Observation's Ref enters the agent's
// accumulating grounding index, so a proposal may cite only facts a tool actually returned.
// A Tool must never mutate state.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Call(ctx context.Context, args json.RawMessage) ([]Observation, error)
}

// ToolRegistry is an ordered (by name), deduplicated set of read-only tools.
type ToolRegistry struct {
	tools  []Tool
	byName map[string]Tool
}

// NewToolRegistry builds a registry, dropping nil/duplicate-named tools and ordering by
// name so Schemas() and dispatch are deterministic.
func NewToolRegistry(tools ...Tool) *ToolRegistry {
	r := &ToolRegistry{byName: make(map[string]Tool)}
	for _, t := range tools {
		if t == nil || t.Name() == "" || r.byName[t.Name()] != nil {
			continue
		}
		r.tools = append(r.tools, t)
		r.byName[t.Name()] = t
	}
	sort.Slice(r.tools, func(i, j int) bool { return r.tools[i].Name() < r.tools[j].Name() })
	return r
}

// Len reports how many tools are registered (0 for a nil registry).
func (r *ToolRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

// Schemas returns the advertised tool schemas in deterministic (name) order.
func (r *ToolRegistry) Schemas() []ToolSchema {
	if r == nil {
		return nil
	}
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: t.Parameters()})
	}
	return out
}

// Names returns the registered tool names in deterministic order (for the prompt + logs).
func (r *ToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Name())
	}
	return out
}

// Call dispatches a tool by name. An unknown tool is an error (not a panic) so the loop
// can surface it as a TOOL ERROR turn without fabricating any ref.
func (r *ToolRegistry) Call(ctx context.Context, name string, args json.RawMessage) ([]Observation, error) {
	if r == nil {
		return nil, fmt.Errorf("no tool registry")
	}
	t := r.byName[name]
	if t == nil {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return t.Call(ctx, args)
}
