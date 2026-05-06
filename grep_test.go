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

func TestGrepStarGlob(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "hello",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	want := "----- /file1.txt - lines from 1 to 2 -----\nhello\nworld"
	if text != want {
		t.Errorf("grep hello *.txt: got %q, want %q", text, want)
	}
}

func TestGrepGlobstar(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "hello",
				"pathspec": "**/*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	want := "----- /file1.txt - lines from 1 to 2 -----\nhello\nworld\n\n----- /subdir/nested.txt - lines from 1 to 2 -----\nhello\nbar"
	if text != want {
		t.Errorf("grep hello **/*.txt: got %q, want %q", text, want)
	}
}

func TestGrepRegex(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "^w",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	want := "----- /file1.txt - lines from 1 to 3 -----\nhello\nworld\nfoo"
	if text != want {
		t.Errorf("grep ^w: got %q, want %q", text, want)
	}
}

func TestGrepNoMatch(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "nonexistent",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	if textOf(t, result) != "" {
		t.Errorf("grep nonexistent: expected empty, got %q", textOf(t, result))
	}
}

func TestGrepLimit500(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 600; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("file%03d.txt", i)), []byte("match"), 0644)
	}
	rootDir = root

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "match",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	got := textOf(t, result)
	if !strings.HasPrefix(got, "Output cut at 500 lines, refine the search pattern\n") {
		t.Fatalf("expected cut prefix, got %q", got[:80])
	}
	contentLines := 0
	for _, line := range strings.Split(got, "\n") {
		if line != "Output cut at 500 lines, refine the search pattern" && !strings.HasPrefix(line, "-----") && line != "" {
			contentLines++
		}
	}
	if contentLines != 500 {
		t.Errorf("expected 500 content lines, got %d", contentLines)
	}
}

func TestGrepContextOverlap(t *testing.T) {
	root := t.TempDir()
	// With ctx=1, matches at distance <= 2*ctxLines merge.
	// match1 at line 3, match2 at line 5. Distance=2 <= 2*1, so they merge.
	// Merged block: from line 2 to line 6.
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("a\nb\nmatch1\nx\nmatch2\ny\nz"), 0644)
	rootDir = root

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "match",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	// Should be a single block, no blank line separator
	if strings.Count(text, "----- /") != 1 {
		t.Errorf("expected single block for overlapping context, got %q", text)
	}
	lines := strings.Split(text, "\n")
	if lines[0] != "----- /test.txt - lines from 2 to 6 -----" {
		t.Errorf("expected merged block header, got %q", lines[0])
	}
}

func TestGrepVisualTab(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "tabs.txt"), []byte("a\tb\nmatch\tc"), 0644)
	rootDir = root

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "match",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	if !strings.Contains(text, "a\tb") {
		t.Errorf("expected raw tab in context line, got %q", text)
	}
	if !strings.Contains(text, "match\tc") {
		t.Errorf("expected raw tab in match line, got %q", text)
	}
}

func TestGrepVisualTrailingSpace(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "spaces.txt"), []byte("match   \nother"), 0644)
	rootDir = root

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "match",
				"pathspec": "*.txt",
			},
		},
	}
	result, err := grepHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("grep returned error")
	}
	text := textOf(t, result)
	if !strings.Contains(text, "match   ") {
		t.Errorf("expected raw trailing spaces, got %q", text)
	}
}
