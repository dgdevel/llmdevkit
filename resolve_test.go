package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func setupTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "subdir"), 0755)
	os.WriteFile(filepath.Join(root, "file1.txt"), []byte("hello\nworld\nfoo"), 0644)
	os.WriteFile(filepath.Join(root, "subdir", "nested.txt"), []byte("hello\nbar"), 0644)
	rootDir = root
	return root
}

func textOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	return tc.Text
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.txt", "file.txt", true},
		{"*.txt", "sub/file.txt", false},
		{"**/*.txt", "file.txt", true},
		{"**/*.txt", "sub/file.txt", true},
		{"**/*.txt", "a/b/file.txt", true},
		{"sub/*.txt", "sub/file.txt", true},
		{"sub/*.txt", "other/file.txt", false},
		{"**", "sub", true},
		{"*", "sub", true},
		{"*", "sub/file.txt", false},
		{"sub/**", "sub/file.txt", true},
		{"sub/**", "sub/a/file.txt", true},
		{"sub/**", "other/file.txt", false},
	}
	for _, tt := range tests {
		got := globMatch(tt.pattern, tt.name)
		if got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	rootDir = root

	tests := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{"/", root, false},
		{".", root, false},
		{"subdir", filepath.Join(root, "subdir"), false},
		{"/subdir", filepath.Join(root, "subdir"), false},
		{"../etc/passwd", "", true},
	}

	for _, tt := range tests {
		got, err := resolve(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolve(%q) error=%v, wantErr=%v", tt.path, err, tt.wantErr)
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("resolve(%q)=%q, want %q", tt.path, got, tt.want)
		}
	}
}
