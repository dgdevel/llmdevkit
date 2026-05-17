package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestSedReplace(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "hello",
				"replacement": "HI",
				"pathspec":    "file1.txt",
			},
		},
	}

	// First call: dry-run
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed dry-run returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "this will edit 1 lines") {
		t.Fatalf("expected dry-run message, got %q", txt)
	}
	// File should not be modified yet
	data, _ := os.ReadFile(filepath.Join(RootDir, "file1.txt"))
	if string(data) != "hello\nworld\nfoo" {
		t.Fatalf("file modified before confirmation, got %q", string(data))
	}

	// Second call: apply
	result, err = SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed apply returned error")
	}
	txt = textOf(t, result)
	if !strings.Contains(txt, "replaced 1 occurrences in 1 files") {
		t.Fatalf("expected apply message, got %q", txt)
	}
	data, _ = os.ReadFile(filepath.Join(RootDir, "file1.txt"))
	if string(data) != "HI\nworld\nfoo" {
		t.Errorf("sed: file content got %q, want %q", string(data), "HI\nworld\nfoo")
	}
}

func TestSedNoChange(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "nonexistent",
				"replacement": "X",
				"pathspec":    "*.txt",
			},
		},
	}
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	if textOf(t, result) != "" {
		t.Errorf("sed no change: expected empty, got %q", textOf(t, result))
	}
}

func TestSedGlobstar(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "hello",
				"replacement": "HEY",
				"pathspec":    "**/*.txt",
			},
		},
	}

	// First call: dry-run
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed dry-run returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "this will edit 2 lines") {
		t.Fatalf("expected dry-run with 2 lines, got %q", txt)
	}

	// Second call: apply
	result, err = SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed apply returned error")
	}
	txt = textOf(t, result)
	if !strings.Contains(txt, "replaced 2 occurrences in 2 files") {
		t.Fatalf("expected apply message, got %q", txt)
	}

	d1, _ := os.ReadFile(filepath.Join(RootDir, "file1.txt"))
	if string(d1) != "HEY\nworld\nfoo" {
		t.Errorf("sed file1.txt: got %q", string(d1))
	}
	d2, _ := os.ReadFile(filepath.Join(RootDir, "subdir", "nested.txt"))
	if string(d2) != "HEY\nbar" {
		t.Errorf("sed nested.txt: got %q", string(d2))
	}
}
