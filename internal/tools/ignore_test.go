package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func setupIgnoreTest(t *testing.T, globs string) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755)
	os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"), []byte("js"), 0644)
	os.WriteFile(filepath.Join(root, "app.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(root, "app_test.go"), []byte("test"), 0644)
	os.MkdirAll(filepath.Join(root, ".git"), 0755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("gitconfig"), 0644)
	RootDir = root
	if globs != "" {
		IgnoreGlobs = strings.Split(globs, ",")
	}
	t.Cleanup(func() {
		IgnoreGlobs = nil
	})
}

func TestIsIgnored(t *testing.T) {
	root := t.TempDir()
	RootDir = root
	IgnoreGlobs = []string{"node_modules"}
	t.Cleanup(func() { IgnoreGlobs = nil })

	if IsIgnored(filepath.Join(root, "app.go")) {
		t.Error("app.go should not be ignored")
	}
	if !IsIgnored(filepath.Join(root, "node_modules")) {
		t.Error("node_modules should be ignored")
	}
	if !IsIgnored(filepath.Join(root, "node_modules", "pkg", "index.js")) {
		t.Error("node_modules/pkg/index.js should be ignored")
	}
}

func TestIsIgnoredNil(t *testing.T) {
	root := t.TempDir()
	RootDir = root
	IgnoreGlobs = nil

	if IsIgnored(filepath.Join(root, "anything")) {
		t.Error("nothing should be ignored when ignoreRe is nil")
	}
}

func TestLsIgnore(t *testing.T) {
	setupIgnoreTest(t, "node_modules")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "**",
			},
		},
	}
	result, err := LsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	text := textOf(t, result)
	if strings.Contains(text, "node_modules") {
		t.Errorf("ls should not list ignored entries, got: %s", text)
	}
}

func TestLsIgnoreDir(t *testing.T) {
	setupIgnoreTest(t, "node_modules")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "**",
			},
		},
	}
	result, err := LsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("find returned error")
	}
	text := textOf(t, result)
	if strings.Contains(text, "node_modules") {
		t.Errorf("ls should skip ignored directory and its contents, got: %s", text)
	}
}

func TestReadIgnored(t *testing.T) {
	setupIgnoreTest(t, "node_modules")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_read",
			Arguments: map[string]interface{}{
				"path":       "node_modules/pkg/index.js",
				"line_range": ":",
			},
		},
	}
	result, err := FileReadHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error reading ignored file")
	}
}

func TestGrepIgnore(t *testing.T) {
	setupIgnoreTest(t, "node_modules")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "js",
				"pathspec": "**",
			},
		},
	}
	result, err := GrepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	if strings.Contains(text, "node_modules") {
		t.Errorf("grep should skip ignored files, got: %s", text)
	}
}

func TestCreateIgnored(t *testing.T) {
	setupIgnoreTest(t, ".git")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    ".git/hooks/pre-commit",
				"content": "#!/bin/sh",
			},
		},
	}
	result, err := CreateHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error creating file in ignored directory")
	}
}

func TestSedIgnored(t *testing.T) {
	setupIgnoreTest(t, ".git")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "sed",
			Arguments: map[string]interface{}{
				"pattern":     "gitconfig",
				"replacement": "changed",
				"pathspec":    ".git/config",
			},
		},
	}
	result, err := SedHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "" {
		t.Error("expected no files changed for ignored path")
	}
}

func TestStatIgnored(t *testing.T) {
	setupIgnoreTest(t, ".git")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": ".git/config",
			},
		},
	}
	result, err := StatHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error stating ignored file")
	}
}

func TestRmIgnored(t *testing.T) {
	setupIgnoreTest(t, ".git")

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": ".git/config",
			},
		},
	}
	result, err := RmHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error removing ignored file")
	}
}

func TestEditIgnored(t *testing.T) {
	root := t.TempDir()
	RootDir = root
	IgnoreGlobs = []string{".git"}
	t.Cleanup(func() { IgnoreGlobs = nil })

	os.MkdirAll(filepath.Join(root, ".git"), 0755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("old\n"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              ".git/config",
				"start_line_number": 1,
				"original_window":   "old",
				"modified_window":   "new",
			},
		},
	}
	result, err := EditHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error editing ignored file")
	}
}
