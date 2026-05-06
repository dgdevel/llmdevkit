package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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
	result, err := sedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	if textOf(t, result) != "file1.txt:1:HI" {
		t.Errorf("sed: got %q, want %q", textOf(t, result), "file1.txt:1:HI")
	}
	data, _ := os.ReadFile(filepath.Join(rootDir, "file1.txt"))
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
	result, err := sedHandler(context.Background(), req)
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
	result, err := sedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	lines := strings.Split(textOf(t, result), "\n")
	sort.Strings(lines)
	want := []string{"file1.txt:1:HEY", "subdir/nested.txt:1:HEY"}
	if len(lines) != len(want) {
		t.Fatalf("sed **/*.txt: got %v, want %v", lines, want)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("sed **/*.txt: got %v, want %v", lines, want)
		}
	}
	d1, _ := os.ReadFile(filepath.Join(rootDir, "file1.txt"))
	if string(d1) != "HEY\nworld\nfoo" {
		t.Errorf("sed file1.txt: got %q", string(d1))
	}
	d2, _ := os.ReadFile(filepath.Join(rootDir, "subdir", "nested.txt"))
	if string(d2) != "HEY\nbar" {
		t.Errorf("sed nested.txt: got %q", string(d2))
	}
}
