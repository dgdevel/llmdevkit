package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRmFile(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": "/file1.txt",
			},
		},
	}
	result, err := rmHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("rm returned error")
	}
	if _, err := os.Stat(filepath.Join(root, "file1.txt")); !os.IsNotExist(err) {
		t.Error("file still exists after rm")
	}
}

func TestRmDir(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": "/subdir",
			},
		},
	}
	result, err := rmHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("rm returned error")
	}
	if _, err := os.Stat(filepath.Join(root, "subdir")); !os.IsNotExist(err) {
		t.Error("directory still exists after rm")
	}
	if _, err := os.Stat(filepath.Join(root, "subdir", "nested.txt")); !os.IsNotExist(err) {
		t.Error("nested file still exists after rm")
	}
}

func TestRmNotFound(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": "/nonexistent",
			},
		},
	}
	result, err := rmHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("rm should not error on nonexistent path")
	}
}

func TestRmEscape(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "rm",
			Arguments: map[string]interface{}{
				"path": "../../etc",
			},
		},
	}
	result, err := rmHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}
