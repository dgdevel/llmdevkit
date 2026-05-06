package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func lsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathspec, err := req.RequireString("pathspec")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if pathspec == "" || pathspec == "." {
		pathspec = "*"
	} else {
		dirRef := strings.TrimSuffix(pathspec, "/")
		if !strings.ContainsAny(dirRef, "*?[") {
			abs, rErr := resolve(dirRef)
			if rErr == nil {
				if info, sErr := os.Stat(abs); sErr == nil && info.IsDir() {
					pathspec = dirRef + "/*"
				}
			}
		}
	}
	var matches []string
	err = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if isConfigPath(path) {
			return nil
		}
		if isIgnored(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if !globMatch(pathspec, rel) {
			return nil
		}
		name := rel
		if d.IsDir() {
			name += "/"
		}
		matches = append(matches, name)
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}
	if matches == nil {
		return mcp.NewToolResultText(""), nil
	}
	var b strings.Builder
	if len(matches) > 500 {
		b.WriteString("Output cut at 500 lines, refine the search pattern\n")
		matches = matches[:500]
	}
	b.WriteString(strings.Join(matches, "\n"))
	return mcp.NewToolResultText(b.String()), nil
}
