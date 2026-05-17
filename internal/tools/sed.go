package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	sedMu           sync.Mutex
	lastSedSignature string
)

func sedSignature(pattern, replacement, pathspec string) string {
	return pattern + "\x00" + replacement + "\x00" + pathspec
}

func SedHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, err := req.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	replacement, err := req.RequireString("replacement")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pathspec, err := req.RequireString("pathspec")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sig := sedSignature(pattern, replacement, pathspec)

	// Determine if this is a confirmation (same inputs as last call)
	sedMu.Lock()
	confirm := lastSedSignature != "" && lastSedSignature == sig
	if !confirm {
		lastSedSignature = sig
	}
	sedMu.Unlock()

	// Gather matching files
	var files []string
	filepath.WalkDir(RootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if IsConfigPath(path) {
			return nil
		}
		if IsIgnored(path) {
			return nil
		}
		rel, err := filepath.Rel(RootDir, path)
		if err != nil {
			return nil
		}
		if GlobMatch(pathspec, rel) {
			files = append(files, path)
		}
		return nil
	})

	// Dry-run: count changed lines per file
	type fileDelta struct {
		relPath string
		count   int
	}
	var deltas []fileDelta
	totalLines := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		newData := re.ReplaceAllLiteral(data, []byte(replacement))
		if string(newData) == string(data) {
			continue
		}
		oldLines := strings.Split(string(data), "\n")
		newLines := strings.Split(string(newData), "\n")
		count := 0
		for i := 0; i < len(newLines); i++ {
			if i >= len(oldLines) || oldLines[i] != newLines[i] {
				count++
			}
		}
		rel, _ := filepath.Rel(RootDir, f)
		deltas = append(deltas, fileDelta{relPath: rel, count: count})
		totalLines += count
	}

	if totalLines == 0 {
		sedMu.Lock()
		lastSedSignature = ""
		sedMu.Unlock()
		return mcp.NewToolResultText(""), nil
	}

	// Over 30 occurrences: refuse
	if totalLines > 30 {
		sedMu.Lock()
		lastSedSignature = ""
		sedMu.Unlock()
		return mcp.NewToolResultError(fmt.Sprintf("too many occurrences (%d across %d files), refusing", totalLines, len(deltas))), nil
	}

	// Not a confirmation: return dry-run message
	if !confirm {
		if len(deltas) > 10 {
			return mcp.NewToolResultText(fmt.Sprintf("this will edit %d occurrences across %d files. to proceed invoke again.", totalLines, len(deltas))), nil
		}
		names := make([]string, len(deltas))
		for i, d := range deltas {
			names[i] = d.relPath
		}
		return mcp.NewToolResultText(fmt.Sprintf("this will edit %d lines across the following files: %s. to proceed invoke again.", totalLines, strings.Join(names, ", "))), nil
	}

	// Confirmation: apply changes
	appliedFiles := 0
	appliedLines := 0
	var out []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		newData := re.ReplaceAllLiteral(data, []byte(replacement))
		if string(newData) == string(data) {
			continue
		}
		if err := os.WriteFile(f, newData, 0644); err != nil {
			continue
		}
		rel, _ := filepath.Rel(RootDir, f)
		oldLines := strings.Split(string(data), "\n")
		newLines := strings.Split(string(newData), "\n")
		for i := 0; i < len(newLines); i++ {
			if i >= len(oldLines) || oldLines[i] != newLines[i] {
				out = append(out, fmt.Sprintf("%s:%d:%s", rel, i+1, newLines[i]))
				appliedLines++
			}
		}
		appliedFiles++
	}

	sedMu.Lock()
	lastSedSignature = ""
	sedMu.Unlock()

	return mcp.NewToolResultText(fmt.Sprintf("replaced %d occurrences in %d files\n%s", appliedLines, appliedFiles, strings.Join(out, "\n"))), nil
}
