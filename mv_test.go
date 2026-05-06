package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMvFile(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("hello"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "/b.txt",
			},
		},
	}
	result, err := mvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mv returned error: %v", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(rootDir, "b.txt"))
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", string(data), "hello")
	}
	if _, err := os.Stat(filepath.Join(rootDir, "a.txt")); !os.IsNotExist(err) {
		t.Error("source file still exists")
	}
}

func TestMvDirectory(t *testing.T) {
	setupTestRoot(t)
	os.MkdirAll(filepath.Join(rootDir, "dir1", "sub"), 0755)
	os.WriteFile(filepath.Join(rootDir, "dir1", "sub", "f.txt"), []byte("data"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/dir1",
				"dest":   "/dir2",
			},
		},
	}
	result, err := mvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mv returned error: %v", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(rootDir, "dir2", "sub", "f.txt"))
	if string(data) != "data" {
		t.Errorf("got %q, want %q", string(data), "data")
	}
}

func TestMvDestExists(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(rootDir, "b.txt"), []byte("b"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "/b.txt",
			},
		},
	}
	result, err := mvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when destination exists")
	}
}

func TestMvSourceNotFound(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/nonexistent.txt",
				"dest":   "/somewhere.txt",
			},
		},
	}
	result, err := mvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when source not found")
	}
}

func TestMvEscape(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(rootDir, "a.txt"), []byte("a"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "../../tmp/x",
			},
		},
	}
	result, err := mvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}
