package dgx_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// fakeTool is a hermetic dgx.Tool returning fixed observations (or an error). Shared by the
// registry + loop tests.
type fakeTool struct {
	name string
	obs  []dgx.Observation
	err  error
}

func (f fakeTool) Name() string        { return f.name }
func (f fakeTool) Description() string { return "fake " + f.name }
func (f fakeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (f fakeTool) Call(context.Context, json.RawMessage) ([]dgx.Observation, error) {
	return f.obs, f.err
}

func TestToolRegistry(t *testing.T) {
	r := dgx.NewToolRegistry(
		fakeTool{name: "b_tool", obs: []dgx.Observation{{Ref: "b:1", Kind: "k", Detail: "d"}}},
		fakeTool{name: "a_tool", obs: []dgx.Observation{{Ref: "a:1"}}},
		nil,                      // dropped
		fakeTool{name: "a_tool"}, // duplicate name dropped
	)
	if r.Len() != 2 {
		t.Fatalf("len = %d, want 2 (nil + dup dropped)", r.Len())
	}
	if names := r.Names(); names[0] != "a_tool" || names[1] != "b_tool" {
		t.Errorf("names not sorted: %v", names)
	}
	if sch := r.Schemas(); len(sch) != 2 || sch[0].Name != "a_tool" {
		t.Errorf("schemas = %+v, want sorted by name", sch)
	}
	obs, err := r.Call(context.Background(), "b_tool", nil)
	if err != nil || len(obs) != 1 || obs[0].Ref != "b:1" {
		t.Errorf("call b_tool = %+v, %v", obs, err)
	}
	if _, err := r.Call(context.Background(), "nope", nil); err == nil {
		t.Error("unknown tool must error, not panic")
	}
	re := dgx.NewToolRegistry(fakeTool{name: "err_tool", err: errors.New("boom")})
	if _, err := re.Call(context.Background(), "err_tool", nil); err == nil {
		t.Error("a tool error must surface as an error")
	}
	// A nil registry is safe.
	var nilReg *dgx.ToolRegistry
	if nilReg.Len() != 0 || nilReg.Schemas() != nil {
		t.Error("nil registry should be empty/safe")
	}
}
