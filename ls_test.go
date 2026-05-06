package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestLsStarGlob(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "*.txt",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	text := textOf(t, result)
	if text != "file1.txt" {
		t.Errorf("ls *.txt: got %q, want %q", text, "file1.txt")
	}
}

func TestLsGlobstar(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "**/*.txt",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	lines := strings.Split(textOf(t, result), "\n")
	sort.Strings(lines)
	want := []string{"file1.txt", "subdir/nested.txt"}
	if len(lines) != len(want) {
		t.Fatalf("ls **/*.txt: got %v, want %v", lines, want)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("ls **/*.txt: got %v, want %v", lines, want)
		}
	}
}

func TestLsDir(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "sub*",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	text := textOf(t, result)
	if text != "subdir/" {
		t.Errorf("ls sub*: got %q, want %q", text, "subdir/")
	}
}

func TestLsNoMatch(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "*.xyz",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	if textOf(t, result) != "" {
		t.Errorf("ls *.xyz: expected empty, got %q", textOf(t, result))
	}
}

func TestLsDotPattern(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": ".",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	lines := strings.Split(textOf(t, result), "\n")
	sort.Strings(lines)
	want := []string{"file1.txt", "subdir/"}
	if len(lines) != len(want) {
		t.Fatalf("ls .: got %v, want %v", lines, want)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("ls .: got %v, want %v", lines, want)
		}
	}
}

func TestLsEmptyPattern(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	lines := strings.Split(textOf(t, result), "\n")
	sort.Strings(lines)
	want := []string{"file1.txt", "subdir/"}
	if len(lines) != len(want) {
		t.Fatalf("ls empty: got %v, want %v", lines, want)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("ls empty: got %v, want %v", lines, want)
		}
	}
}

func TestLsDirectoryWithSlash(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "subdir/",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	text := textOf(t, result)
	if text != "subdir/nested.txt" {
		t.Errorf("ls subdir/: got %q, want %q", text, "subdir/nested.txt")
	}
}

func TestLsDirectoryWithoutSlash(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "subdir",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	text := textOf(t, result)
	if text != "subdir/nested.txt" {
		t.Errorf("ls subdir: got %q, want %q", text, "subdir/nested.txt")
	}
}

func TestLsLimit500(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 600; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%03d.txt", i)), []byte("x"), 0644)
	}
	rootDir = root

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "ls",
			Arguments: map[string]interface{}{
				"pathspec": "*.txt",
			},
		},
	}
	result, err := lsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("ls returned error")
	}
	got := textOf(t, result)
	if !strings.HasPrefix(got, "Output cut at 500 lines, refine the search pattern\n") {
		t.Fatalf("expected cut prefix, got %q", got[:80])
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 501 {
		t.Errorf("expected 501 lines, got %d", len(lines))
	}
}
