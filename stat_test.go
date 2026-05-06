package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestStatFile(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": "/file1.txt",
			},
		},
	}
	result, err := statHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("stat returned error")
	}
	text := textOf(t, result)
	if !strings.Contains(text, "Type: file\n") {
		t.Errorf("stat file: missing Type: file in %q", text)
	}
	if !strings.Contains(text, "Size: ") {
		t.Errorf("stat file: missing Size in %q", text)
	}
	if !strings.Contains(text, "Permissions: ") {
		t.Errorf("stat file: missing Permissions in %q", text)
	}
	if !strings.Contains(text, "Owner: ") {
		t.Errorf("stat file: missing Owner in %q", text)
	}
	if !strings.Contains(text, "Group: ") {
		t.Errorf("stat file: missing Group in %q", text)
	}
	if !strings.Contains(text, "Modify: ") {
		t.Errorf("stat file: missing Modify in %q", text)
	}
	if !strings.Contains(text, "Birth: ") || strings.Contains(text, "Birth: \n") {
		t.Errorf("stat file: missing Birth in %q", text)
	}
	info, _ := os.Stat(filepath.Join(root, "file1.txt"))
	if !strings.Contains(text, fmt.Sprintf("Size: %d,", info.Size())) {
		t.Errorf("stat file: size mismatch in %q", text)
	}
}

func TestStatDir(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": "/subdir",
			},
		},
	}
	result, err := statHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("stat returned error")
	}
	text := textOf(t, result)
	if !strings.Contains(text, "Type: directory\n") {
		t.Errorf("stat dir: missing Type: directory in %q", text)
	}
}

func TestStatNotFound(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": "/nonexistent",
			},
		},
	}
	result, err := statHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent path")
	}
}

func TestStatEscape(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "stat",
			Arguments: map[string]interface{}{
				"path": "../../etc/passwd",
			},
		},
	}
	result, err := statHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
