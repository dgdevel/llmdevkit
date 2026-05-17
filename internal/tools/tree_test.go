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

	if lines[0] != root {
		t.Errorf("first line should be root dir, got: %s", lines[0])
	}
	if !strings.Contains(text, "├── a") || !strings.Contains(text, "└── d") {
		t.Errorf("expected tree output, got:\n%s", text)
	}
	if !strings.Contains(text, "├── b") || !strings.Contains(text, "└── c") {
		t.Errorf("expected nested dirs, got:\n%s", text)
	}
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
	if !strings.Contains(text, "src") {
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
	if text != root {
		t.Errorf("empty root should just show root, got: %s", text)
	}
}
