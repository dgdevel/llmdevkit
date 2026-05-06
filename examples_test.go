package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestExamplesKnownTool(t *testing.T) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "examples",
			Arguments: map[string]interface{}{
				"tool_name": "ls",
			},
		},
	}
	result, err := examplesHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("examples returned error for known tool")
	}
	got := textOf(t, result)
	if !strings.Contains(got, "Example 1") || !strings.Contains(got, "Example 2") || !strings.Contains(got, "Example 3") {
		t.Errorf("expected at least 3 examples, got: %s", got[:100])
	}
}

func TestExamplesUnknownTool(t *testing.T) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "examples",
			Arguments: map[string]interface{}{
				"tool_name": "nonexistent",
			},
		},
	}
	result, err := examplesHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	got := textOf(t, result)
	if !strings.Contains(got, "Available:") {
		t.Errorf("expected available tools list, got: %s", got)
	}
}

func TestExamplesAllToolsHaveEntries(t *testing.T) {
	tools := []string{
		"ls", "file_read", "file_create", "mv", "grep", "sed",
		"edit", "rm", "stat",
		"tasks_list", "task_create", "task_set_status", "task_delete", "tasks_clear",
		"w3m-dump", "online_search", "available_commands", "run_command", "examples",
	}
	for _, name := range tools {
		ex, ok := toolExamples[name]
		if !ok {
			t.Errorf("missing examples for tool %q", name)
			continue
		}
		count := strings.Count(ex, "Example ")
		if count < 3 {
			t.Errorf("tool %q has %d examples, want at least 3", name, count)
		}
	}
}
