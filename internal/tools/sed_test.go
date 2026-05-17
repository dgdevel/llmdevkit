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

	// First call: executes immediately (≤ 30 occurrences)
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "replaced 1 occurrences in 1 files") {
		t.Fatalf("expected apply message, got %q", txt)
	}
	// File should be modified
	data, _ := os.ReadFile(filepath.Join(RootDir, "file1.txt"))
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

	// First call: executes immediately (≤ 30 occurrences)
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	txt := textOf(t, result)
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

func TestSedLargeConfirmation(t *testing.T) {
	setupTestRoot(t)

	// Create many files with multiple matches each to exceed 30 occurrences
	for i := 0; i < 15; i++ {
		filename := filepath.Join(RootDir, "batch", "file"+string(rune('A'+i))+".txt")
		os.MkdirAll(filepath.Dir(filename), 0755)
		content := strings.Repeat("hello\n", 3) // 3 matches per file = 45 total
		os.WriteFile(filename, []byte(content), 0644)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "hello",
				"replacement": "HI",
				"pathspec":    "batch/*.txt",
			},
		},
	}

	// First call: should ask for confirmation (> 30 occurrences)
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed dry-run returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "this will edit 45 occurrences") {
		t.Fatalf("expected confirmation message, got %q", txt)
	}

	// Files should not be modified yet
	data, _ := os.ReadFile(filepath.Join(RootDir, "batch", "fileA.txt"))
	if strings.Contains(string(data), "HI") {
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
	if !strings.Contains(txt, "replaced 45 occurrences in 15 files") {
		t.Fatalf("expected apply message, got %q", txt)
	}

	// Verify file was modified
	data, _ = os.ReadFile(filepath.Join(RootDir, "batch", "fileA.txt"))
	if !strings.Contains(string(data), "HI") {
		t.Errorf("sed batch file: got %q", string(data))
	}
}

func TestSedLargeFileList(t *testing.T) {
	setupTestRoot(t)

	// Create 5 files with multiple matches each to exceed 30 occurrences but ≤ 10 files
	for i := 0; i < 5; i++ {
		filename := filepath.Join(RootDir, "multi", "file"+string(rune('A'+i))+".txt")
		os.MkdirAll(filepath.Dir(filename), 0755)
		content := strings.Repeat("hello\n", 8) // 8 matches per file = 40 total
		os.WriteFile(filename, []byte(content), 0644)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "hello",
				"replacement": "HI",
				"pathspec":    "multi/*.txt",
			},
		},
	}

	// First call: should show file list (> 30 occurrences, ≤ 10 files)
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed dry-run returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "this will edit 40 occurrences across 5 files:") {
		t.Fatalf("expected confirmation with file list, got %q", txt)
	}
	// Should contain file names
	if !strings.Contains(txt, "multi/fileA.txt") {
		t.Fatalf("expected file names in message, got %q", txt)
	}
}

func TestSedLargeFileCount(t *testing.T) {
	setupTestRoot(t)

	// Create 15 files with one match each (15 occurrences - under 30, should execute immediately)
	for i := 0; i < 15; i++ {
		filename := filepath.Join(RootDir, "many", "file"+string(rune('A'+i))+".txt")
		os.MkdirAll(filepath.Dir(filename), 0755)
		os.WriteFile(filename, []byte("hello\nworld"), 0644)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "hello",
				"replacement": "HI",
				"pathspec":    "many/*.txt",
			},
		},
	}

	// Should execute immediately (15 occurrences ≤ 30)
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("sed returned error")
	}
	txt := textOf(t, result)
	if !strings.Contains(txt, "replaced 15 occurrences in 15 files") {
		t.Fatalf("expected immediate apply, got %q", txt)
	}
}
