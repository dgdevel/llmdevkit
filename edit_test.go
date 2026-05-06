package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestEditExactMatch(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 2,
				"original_window":   "world",
				"modified_window":   "earth",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("edit returned error: %s", textOf(t, result))
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hello\nearth\nfoo\n" {
		t.Errorf("edit exact: got %q", string(data))
	}
}

func TestEditStartLineOffBy1(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 3,
				"original_window":   "world",
				"modified_window":   "earth",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("edit returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text != "done, start_line_number was wrong, it was 2 instead" {
		t.Errorf("expected corrected line msg, got %q", text)
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hello\nearth\nfoo\n" {
		t.Errorf("edit off-by-1: got %q", string(data))
	}
}

func TestEditStartLineOffBy5(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 7,
				"original_window":   "world",
				"modified_window":   "earth",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("edit returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text != "done, start_line_number was wrong, it was 2 instead" {
		t.Errorf("expected corrected line msg, got %q", text)
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hello\nearth\nfoo\n" {
		t.Errorf("edit off-by-5: got %q", string(data))
	}
}

func TestEditStartLineOffBy6(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 8,
				"original_window":   "world",
				"modified_window":   "earth",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("edit should not return MCP error for ko")
	}
	text := textOf(t, result)
	if text != "ko: no match (start_line_number 8 too far from actual match at 2)" {
		t.Errorf("expected ko message, got %q", text)
	}
}

func TestEditNoMatch(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 1,
				"original_window":   "nonexistent",
				"modified_window":   "replacement",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	if text != "ko: no match" {
		t.Errorf("expected ko: no match, got %q", text)
	}
}

func TestEditMultipleMatches(t *testing.T) {
	root := setupTestRoot(t)
	os.WriteFile(filepath.Join(root, "dup.txt"), []byte("hello\nhello\nhello"), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/dup.txt",
				"start_line_number": 50,
				"original_window":   "hello",
				"modified_window":   "world",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := textOf(t, result)
	if text != "ko: 3 matches found, ensure start_line_number is right" {
		t.Errorf("expected multiple matches ko, got %q", text)
	}
}

func TestEditMultipleMatchesWithCorrectStartLine(t *testing.T) {
	root := setupTestRoot(t)
	content := "hello\na\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\nhello"
	os.WriteFile(filepath.Join(root, "dup.txt"), []byte(content), 0644)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/dup.txt",
				"start_line_number": 1,
				"original_window":   "hello",
				"modified_window":   "bye",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("edit returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text != "done" {
		t.Errorf("expected done, got %q", text)
	}
	data, _ := os.ReadFile(filepath.Join(root, "dup.txt"))
	expected := "bye\na\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\nhello\n"
	if string(data) != expected {
		t.Errorf("edit multi-match with correct start: got %q", string(data))
	}
}

func TestEditMultiLineWindow(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 1,
				"original_window":   "hello\nworld",
				"modified_window":   "hi\nearth",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hi\nearth\nfoo\n" {
		t.Errorf("edit multiline: got %q", string(data))
	}
}

func TestEditReplaceWithMoreLines(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 2,
				"original_window":   "world",
				"modified_window":   "earth\nmars",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hello\nearth\nmars\nfoo\n" {
		t.Errorf("edit expand: got %q", string(data))
	}
}

func TestEditReplaceWithFewerLines(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 1,
				"original_window":   "hello\nworld",
				"modified_window":   "hi",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hi\nfoo\n" {
		t.Errorf("edit shrink: got %q", string(data))
	}
}

func TestEditEmptyModifiedWindow(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/file1.txt",
				"start_line_number": 2,
				"original_window":   "world",
				"modified_window":   "",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "file1.txt"))
	if string(data) != "hello\nfoo\n" {
		t.Errorf("edit delete: got %q", string(data))
	}
}

func TestEditPathEscape(t *testing.T) {
	setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "../../etc/passwd",
				"start_line_number": 1,
				"original_window":   "x",
				"modified_window":   "y",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}

func TestEditNestedFile(t *testing.T) {
	root := setupTestRoot(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "edit",
			Arguments: map[string]interface{}{
				"path":              "/subdir/nested.txt",
				"start_line_number": 2,
				"original_window":   "bar",
				"modified_window":   "baz",
			},
		},
	}
	result, err := editHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(t, result) != "done" {
		t.Errorf("expected done, got %q", textOf(t, result))
	}
	data, _ := os.ReadFile(filepath.Join(root, "subdir", "nested.txt"))
	if string(data) != "hello\nbaz\n" {
		t.Errorf("edit nested: got %q", string(data))
	}
}
