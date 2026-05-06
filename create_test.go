package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestCreateFile(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "/newfile.txt",
				"content": "new content",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("create returned error")
	}
	data, _ := os.ReadFile(filepath.Join(root, "newfile.txt"))
	if string(data) != "new content" {
		t.Errorf("create: got %q, want %q", string(data), "new content")
	}
}

func TestCreateExisting(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "/file1.txt",
				"content": "overwrite",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when file already exists")
	}
}

func TestCreateEscape(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "../../etc/evil",
				"content": "x",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}

func TestCreateNested(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "/subdir/new.txt",
				"content": "nested",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("create returned error")
	}
	data, _ := os.ReadFile(filepath.Join(root, "subdir", "new.txt"))
	if string(data) != "nested" {
		t.Errorf("create nested: got %q, want %q", string(data), "nested")
	}
}

func TestCreateMkdirAll(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_create",
			Arguments: map[string]interface{}{
				"path":    "/a/b/c/deep.txt",
				"content": "deep",
			},
		},
	}
	result, err := createHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("create returned error")
	}
	data, _ := os.ReadFile(filepath.Join(root, "a", "b", "c", "deep.txt"))
	if string(data) != "deep" {
		t.Errorf("create mkdirall: got %q, want %q", string(data), "deep")
	}
}
