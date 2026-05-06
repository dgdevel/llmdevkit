package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMaskPath(t *testing.T) {
	tests := []struct {
		rootDir string
		input   string
		want    string
	}{
		{"/tmp/test", "open /tmp/test/file.txt: no such file or directory", "open /file.txt: no such file or directory"},
		{"/tmp/test", "stat /tmp/test: is a directory", "stat /: is a directory"},
		{"/tmp/test", "read /tmp/test/subdir/nested: permission denied", "read /subdir/nested: permission denied"},
		{"/tmp/test", "path escapes root", "path escapes root"},
		{"/tmp/test", "access denied", "access denied"},
		{"", "open /any/path: error", "open /any/path: error"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			rootDir = tt.rootDir
			got := maskPath(tt.input)
			if got != tt.want {
				t.Errorf("maskPath(%q) with rootDir=%q = %q, want %q", tt.input, tt.rootDir, got, tt.want)
			}
		})
	}
}

func assertNoLeak(t *testing.T, result *mcp.CallToolResult, root string) {
	t.Helper()
	if !result.IsError {
		t.Fatal("expected error result")
	}
	text := textOf(t, result)
	if strings.Contains(text, root) {
		t.Errorf("error message leaks absolute path %q in: %q", root, text)
	}
}

func TestReadErrorMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_read",
			Arguments: map[string]interface{}{
				"path":       "/subdir",
				"line_range": ":",
			},
		},
	}
	result, _ := fileReadHandler(context.Background(), req)
	assertNoLeak(t, result, root)
}

func TestReadNonexistentMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_read",
			Arguments: map[string]interface{}{
				"path":       "/nonexistent.txt",
				"line_range": ":",
			},
		},
	}
	result, _ := fileReadHandler(context.Background(), req)
	assertNoLeak(t, result, root)
}

func TestCreateErrorMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "/file1.txt/nested/file.txt",
				"content": "data",
			},
		},
	}
	result, _ := createHandler(context.Background(), req)
	assertNoLeak(t, result, root)
}

func TestStatErrorMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": "/nonexistent",
			},
		},
	}
	result, _ := statHandler(context.Background(), req)
	assertNoLeak(t, result, root)
}

func TestEditErrorMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/nonexistent.txt",
				"start_line_number": 1,
				"original_window":   "old",
				"modified_window":   "new",
			},
		},
	}
	result, _ := editHandler(context.Background(), req)
	assertNoLeak(t, result, root)
}

func TestRmErrorMasksPath(t *testing.T) {
	root := setupTestRoot(t)
	dir := filepath.Join(root, "protected")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("data"), 0644)
	os.Chmod(dir, 0555)
	defer os.Chmod(dir, 0755)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": "/protected/inner.txt",
			},
		},
	}
	result, _ := rmHandler(context.Background(), req)
	if result.IsError {
		assertNoLeak(t, result, root)
	}
}
