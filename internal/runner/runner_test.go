package runner

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCallTool_ArgsAsObject(t *testing.T) {
	registry := NewToolRegistry()
	registry.Add(&ToolDef{
		Name:        "echo",
		Description: "echo args",
		InputSchema: nil,
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			b, _ := json.Marshal(args)
			return string(b), nil
		},
	})

	args := json.RawMessage(`{"pathspec":"*.go"}`)
	result, err := registry.CallTool(context.Background(), "echo", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != `{"pathspec":"*.go"}` {
		t.Errorf("expected object result, got %s", result)
	}
}

func TestCallTool_ArgsAsString(t *testing.T) {
	registry := NewToolRegistry()
	registry.Add(&ToolDef{
		Name:        "echo",
		Description: "echo args",
		InputSchema: nil,
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			b, _ := json.Marshal(args)
			return string(b), nil
		},
	})

	// Simulate an LLM that wraps args in a JSON string (double-encoded)
	args := json.RawMessage(`"{\"pathspec\":\"*.go\"}"`)
	result, err := registry.CallTool(context.Background(), "echo", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != `{"pathspec":"*.go"}` {
		t.Errorf("expected unwrapped object result, got %s", result)
	}
}

func TestCallTool_ArgsAsNonObjectString(t *testing.T) {
	registry := NewToolRegistry()
	registry.Add(&ToolDef{
		Name:        "fail",
		Description: "should not be called",
		InputSchema: nil,
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			return "should not reach here", nil
		},
	})

	// A plain string that isn't a JSON object
	args := json.RawMessage(`"just a string"`)
	_, err := registry.CallTool(context.Background(), "fail", args)
	if err == nil {
		t.Fatal("expected error for non-object string args")
	}
	t.Logf("got expected error: %v", err)
}

func TestCallTool_ArgsEmpty(t *testing.T) {
	registry := NewToolRegistry()
	registry.Add(&ToolDef{
		Name:        "noop",
		Description: "no args needed",
		InputSchema: nil,
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			if args != nil {
				t.Errorf("expected nil args, got %v", args)
			}
			return "ok", nil
		},
	})

	result, err := registry.CallTool(context.Background(), "noop", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected ok, got %s", result)
	}
}

func TestCallTool_ToolNotFound(t *testing.T) {
	registry := NewToolRegistry()
	_, err := registry.CallTool(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}
