package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func LsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathspec, err := req.RequireString("pathspec")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if pathspec == "" || pathspec == "." {
		pathspec = "*"
	} else {
		dirRef := strings.TrimSuffix(pathspec, "/")
		if !strings.ContainsAny(dirRef, "*?[") {
			abs, rErr := Resolve(dirRef)
			if rErr == nil {
				if info, sErr := os.Stat(abs); sErr == nil && info.IsDir() {
					pathspec = dirRef + "/*"
				}
			}
		}
	}

	type entry struct {
		rel   string
		isDir bool
		info  os.FileInfo
	}
	var entries []entry

	err = filepath.WalkDir(RootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if IsConfigPath(path) {
			return nil
		}
		if IsIgnored(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(RootDir, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if !GlobMatch(pathspec, rel) {
			return nil
		}
		info, _ := d.Info()
		entries = append(entries, entry{rel: rel, isDir: d.IsDir(), info: info})
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(MaskPath(err.Error())), nil
	}
	if entries == nil {
		return mcp.NewToolResultText(""), nil
	}

	cut := len(entries)
	if cut > 500 {
		cut = 500
	}

	var b strings.Builder
	if len(entries) > 500 {
		b.WriteString("Output cut at 500 lines, refine the search pattern\n")
	}

	for _, e := range entries[:cut] {
		if e.isDir {
			b.WriteString(e.rel + "/")
		} else {
			sizeStr := "?"
			lineStr := "?"
			if e.info != nil {
				sizeStr = formatEntrySize(e.info.Size())
				abs, rErr := Resolve(e.rel)
				if rErr == nil {
					if data, readErr := os.ReadFile(abs); readErr == nil {
						if isBinary(data) {
							lineStr = "binary"
						} else {
							lineStr = fmt.Sprintf("%d lines", countLines(data))
						}
					}
				}
			}
			fmt.Fprintf(&b, "%s, %s, %s", sizeStr, lineStr, e.rel)
		}
		b.WriteByte('\n')
	}

	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}
