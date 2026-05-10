package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMvFile(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(RootDir, "a.txt"), []byte("hello"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "/b.txt",
			},
		},
	}
	result, err := MvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mv returned error: %v", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(RootDir, "b.txt"))
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", string(data), "hello")
	}
	if _, err := os.Stat(filepath.Join(RootDir, "a.txt")); !os.IsNotExist(err) {
		t.Error("source file still exists")
	}
}

func TestMvDirectory(t *testing.T) {
	setupTestRoot(t)
	os.MkdirAll(filepath.Join(RootDir, "dir1", "sub"), 0755)
	os.WriteFile(filepath.Join(RootDir, "dir1", "sub", "f.txt"), []byte("data"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/dir1",
				"dest":   "/dir2",
			},
		},
	}
	result, err := MvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("mv returned error: %v", result.Content)
	}
	data, _ := os.ReadFile(filepath.Join(RootDir, "dir2", "sub", "f.txt"))
	if string(data) != "data" {
		t.Errorf("got %q, want %q", string(data), "data")
	}
}

func TestMvDestExists(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(RootDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(RootDir, "b.txt"), []byte("b"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "/b.txt",
			},
		},
	}
	result, err := MvHandler(context.Background(), req)
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
	result, err := MvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when source not found")
	}
}

func TestMvEscape(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(RootDir, "a.txt"), []byte("a"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "mv",
			Arguments: map[string]interface{}{
				"source": "/a.txt",
				"dest":   "../../tmp/x",
			},
		},
	}
	result, err := MvHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}
