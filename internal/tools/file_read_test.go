package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llmdevkit/internal/cfg"

	"github.com/mark3labs/mcp-go/mcp"
)

func fileReadReq(path, lineRange string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "file_read",
			Arguments: map[string]interface{}{
				"path":       path,
				"line_range": lineRange,
			},
		},
	}
}

func TestFileReadBasic(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 1 to 3 -----\nhello\nworld\nfoo\n----- /file1.txt - EOF -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead basic: got %q, want %q", got, want)
	}
}

func TestFileReadRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "2:2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 2 to 2 -----\nworld\n----- /file1.txt - remaining lines from 3 to 3 -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead range: got %q, want %q", got, want)
	}
}

func TestFileReadEmptyLines(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(RootDir, "emptylines.txt"), []byte("a\n\nb"), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/emptylines.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /emptylines.txt - lines from 1 to 3 -----\na\n\nb\n----- /emptylines.txt - EOF -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead empty lines: got %q, want %q", got, want)
	}
}

func TestFileReadPathEscape(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("../../etc/passwd", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for path escape")
	}
}

func TestFileReadConfigPath(t *testing.T) {
	setupTestRoot(t)
	cfgDir := cfg.DirPath(RootDir)
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.ini"), []byte("[core]\nreadonly=true\n"), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/.llmdevkit/config.ini", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for config path")
	}
}

func TestFileReadOutOfRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "100:"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	got := result.Content[0].(mcp.TextContent).Text
	want := "warning: start 100 exceeds total lines 3"
	if got != want {
		t.Errorf("fileRead out of range: expected %q, got %q", want, got)
	}
}

func TestFileReadNoTransform(t *testing.T) {
	setupTestRoot(t)
	os.WriteFile(filepath.Join(RootDir, "raw.txt"), []byte("a\tb\tc\nhello   \nworld  "), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/raw.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /raw.txt - lines from 1 to 3 -----\na\tb\tc\nhello   \nworld  \n----- /raw.txt - EOF -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead no transform: got %q, want %q", got, want)
	}
}

func TestFileReadBlocks(t *testing.T) {
	setupTestRoot(t)
	var buf strings.Builder
	for i := 0; i < 75; i++ {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "line%d", i)
	}
	os.WriteFile(filepath.Join(RootDir, "blocked.txt"), []byte(buf.String()), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/blocked.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	got := result.Content[0].(mcp.TextContent).Text

	if !strings.Contains(got, "----- /blocked.txt - lines from 1 to 75 -----\n") {
		t.Error("missing block header")
	}
	if !strings.Contains(got, "----- /blocked.txt - EOF -----\n") {
		t.Error("missing EOF marker")
	}
	if !strings.Contains(got, "line0\n") {
		t.Error("missing first line content")
	}
	if !strings.Contains(got, "line74\n") {
		t.Error("missing last line content")
	}
}

func TestFileReadCustomBlockSize(t *testing.T) {
	setupTestRoot(t)

	cfgDir := cfg.DirPath(RootDir)
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.ini"), []byte("[core]\nfile_read_block_size=10\n"), 0644)

	var buf strings.Builder
	for i := 0; i < 25; i++ {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "line%d", i)
	}
	os.WriteFile(filepath.Join(RootDir, "custom.txt"), []byte(buf.String()), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/custom.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	got := result.Content[0].(mcp.TextContent).Text

	if !strings.Contains(got, "----- /custom.txt - lines from 1 to 10 -----\n") {
		t.Error("missing block 1 header with custom size")
	}
	if !strings.Contains(got, "----- /custom.txt - lines from 11 to 20 -----\n") {
		t.Error("missing block 2 header with custom size")
	}
	if !strings.Contains(got, "----- /custom.txt - lines from 21 to 25 -----\n") {
		t.Error("missing block 3 header with custom size")
	}
}

func TestFileReadExactBlockBoundary(t *testing.T) {
	setupTestRoot(t)

	cfgDir := cfg.DirPath(RootDir)
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.ini"), []byte("[core]\nfile_read_block_size=3\n"), 0644)

	os.WriteFile(filepath.Join(RootDir, "exact.txt"), []byte("a\nb\nc\nd\ne\nf"), 0644)

	result, err := FileReadHandler(context.Background(), fileReadReq("/exact.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /exact.txt - lines from 1 to 3 -----\na\nb\nc\n----- /exact.txt - lines from 4 to 6 -----\nd\ne\nf\n----- /exact.txt - EOF -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead exact boundary: got %q, want %q", got, want)
	}
}

func TestFileReadNestedPath(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/subdir/nested.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /subdir/nested.txt - lines from 1 to 2 -----\nhello\nbar\n----- /subdir/nested.txt - EOF -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead nested: got %q, want %q", got, want)
	}
}

func TestFileReadGrepBlockCompatibility(t *testing.T) {
	setupTestRoot(t)
	content := "alpha\nbeta\ngamma\ndelta\ngamma\nepsilon"
	os.WriteFile(filepath.Join(RootDir, "grepcompat.txt"), []byte(content), 0644)

	grepResult, err := GrepHandler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "grep",
			Arguments: map[string]interface{}{
				"pattern":  "gamma",
				"pathspec": "grepcompat.txt",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grepResult.IsError {
		t.Fatal("grep returned error")
	}
	grepText := textOf(t, grepResult)

	// Verify grep output contains file_read-style block header
	if !strings.HasPrefix(grepText, "----- /grepcompat.txt - lines from ") {
		t.Fatalf("expected block header prefix, got %q", grepText[:60])
	}

	// Verify "gamma" appears in the output (two occurrences)
	gammaCount := strings.Count(grepText, "gamma")
	if gammaCount != 2 {
		t.Errorf("expected 2 occurrences of gamma, got %d", gammaCount)
	}

	// Read the full file via file_read and verify grep content is a subset
	fileReadResult, err := FileReadHandler(context.Background(), fileReadReq("/grepcompat.txt", ":"))
	if err != nil {
		t.Fatal(err)
	}
	fileReadText := textOf(t, fileReadResult)

	// Every content line in grep output must appear in file_read output
	grepLines := strings.Split(grepText, "\n")
	for _, gl := range grepLines {
		if strings.HasPrefix(gl, "-----") || gl == "" {
			continue
		}
		if !strings.Contains(fileReadText, gl) {
			t.Errorf("grep line %q not found in file_read output", gl)
		}
	}
}

func TestFileRead1Indexed(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 1 to 1 -----\nhello\n----- /file1.txt - remaining lines from 2 to 3 -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead 1-indexed line 1: got %q, want %q", got, want)
	}

	result, err = FileReadHandler(context.Background(), fileReadReq("/file1.txt", "3:3"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want = "----- /file1.txt - lines from 3 to 3 -----\nfoo\n----- /file1.txt - EOF -----\n"
	got = result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead 1-indexed line 3: got %q, want %q", got, want)
	}
}

func TestFileReadDashRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "1-2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 1 to 2 -----\nhello\nworld\n----- /file1.txt - remaining lines from 3 to 3 -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead dash range: got %q, want %q", got, want)
	}
}

func TestFileReadBracketColonRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "[1:2]"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 1 to 2 -----\nhello\nworld\n----- /file1.txt - remaining lines from 3 to 3 -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead bracket colon range: got %q, want %q", got, want)
	}
}

func TestFileReadBracketDashRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "[1-2]"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	want := "----- /file1.txt - lines from 1 to 2 -----\nhello\nworld\n----- /file1.txt - remaining lines from 3 to 3 -----\n"
	got := result.Content[0].(mcp.TextContent).Text
	if got != want {
		t.Errorf("fileRead bracket dash range: got %q, want %q", got, want)
	}
}

func TestFileReadOutOfRangeEnd(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "1-100"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("fileRead returned error")
	}
	got := result.Content[0].(mcp.TextContent).Text
	if !strings.HasPrefix(got, "warning:") {
		t.Errorf("expected warning prefix, got %q", got)
	}
	if !strings.Contains(got, "end 100 exceeds total lines 3") {
		t.Errorf("expected end out of range warning, got %q", got)
	}
}

func TestFileReadInvalidSyntax(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "abc"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid syntax")
	}
	got := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(got, "error: invalid line_range syntax") {
		t.Errorf("expected syntax error, got %q", got)
	}
}

func TestFileReadZeroInfixRange(t *testing.T) {
	setupTestRoot(t)

	result, err := FileReadHandler(context.Background(), fileReadReq("/file1.txt", "0:2"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for zero start")
	}
	got := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(got, "error:") {
		t.Errorf("expected error, got %q", got)
	}
}
