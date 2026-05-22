package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestTreeBasic(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a", "b"), 0755)
	os.MkdirAll(filepath.Join(root, "a", "c"), 0755)
	os.MkdirAll(filepath.Join(root, "d"), 0755)
	os.WriteFile(filepath.Join(root, "a", "f.txt"), []byte("hi"), 0644)

	RootDir = root
	IgnoreGlobs = nil

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tree"}}
	result, err := TreeHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	lines := strings.Split(text, "\n")

	if lines[0] != "/" {
		t.Errorf("first line should be '/', got: %s", lines[0])
	}
	if !strings.Contains(text, "\u251C\u2500\u2500 a/") || !strings.Contains(text, "\u2514\u2500\u2500 d/") {
		t.Errorf("expected tree output, got:\n%s", text)
	}
	if !strings.Contains(text, "\u251C\u2500\u2500 b/") || !strings.Contains(text, "\u2514\u2500\u2500 c/") {
		t.Errorf("expected nested dirs, got:\n%s", text)
	}
	// without with_files, files should not appear
	if strings.Contains(text, "f.txt") {
		t.Error("files should not appear in tree output")
	}
}

func TestTreeIgnore(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0755)
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755)

	RootDir = root
	IgnoreGlobs = []string{"node_modules"}
	t.Cleanup(func() { IgnoreGlobs = nil })

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tree"}}
	result, err := TreeHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "node_modules") {
		t.Errorf("tree should not show ignored dirs, got:\n%s", text)
	}
	if !strings.Contains(text, "src/") {
		t.Errorf("tree should show non-ignored dirs, got:\n%s", text)
	}
}

func TestTreeEmpty(t *testing.T) {
	root := t.TempDir()
	RootDir = root
	IgnoreGlobs = nil

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tree"}}
	result, err := TreeHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if text != "/" {
		t.Errorf("empty root should just show '/', got: %s", text)
	}
}

func TestTreeWithFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0755)
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(root, "src", "util.go"), []byte("package src"), 0644)

	RootDir = root
	IgnoreGlobs = nil

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tree", Arguments: map[string]interface{}{"with_files": true}}}
	result, err := TreeHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "main.go") || !strings.Contains(text, "src/") {
		t.Errorf("expected files and dirs with trailing slash, got:\n%s", text)
	}
	if !strings.Contains(text, "util.go") {
		t.Errorf("expected nested file, got:\n%s", text)
	}
	// Check format: size, lines, filename
	if !strings.Contains(text, "12b, 0 lines, main.go") {
		t.Errorf("expected formatted file entry, got:\n%s", text)
	}
}
