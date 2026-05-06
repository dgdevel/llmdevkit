package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func grepHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, err := req.RequireString("pattern")
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
	var files []string
	filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if isConfigPath(path) {
			return nil
		}
		if isIgnored(path) {
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		if globMatch(pathspec, rel) {
			files = append(files, path)
		}
		return nil
	})

	const ctxLines = 1

	type matchBlock struct{ start, end int }
	type fileResult struct {
		relPath string
		lines   []string
		blocks  []matchBlock
	}

	const binaryCheckSize = 8192

	var results []fileResult
	for _, f := range files {
		rel, _ := filepath.Rel(rootDir, f)
		fh, err := os.Open(f)
		if err != nil {
			continue
		}

		// Binary file check: skip files containing null bytes
		buf := make([]byte, binaryCheckSize)
		n, _ := fh.Read(buf)
		if bytes.IndexByte(buf[:n], 0) >= 0 {
			fh.Close()
			continue
		}
		_, _ = fh.Seek(0, 0)

		var lines []string
		scanner := bufio.NewScanner(fh)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		fh.Close()

		var matchNums []int
		for i, line := range lines {
			if re.MatchString(line) {
				matchNums = append(matchNums, i)
			}
		}
		if len(matchNums) == 0 {
			continue
		}

		var blocks []matchBlock
		bs, be := matchNums[0], matchNums[0]
		for _, n := range matchNums[1:] {
			if n <= be+2*ctxLines {
				be = n
			} else {
				blocks = append(blocks, matchBlock{bs, be})
				bs, be = n, n
			}
		}
		blocks = append(blocks, matchBlock{bs, be})

		results = append(results, fileResult{relPath: rel, lines: lines, blocks: blocks})
	}

	if len(results) == 0 {
		return mcp.NewToolResultText(""), nil
	}

	var b strings.Builder
	totalLines := 0
	cut := false

	for _, fr := range results {
		for _, block := range fr.blocks {
			from := block.start - ctxLines
			if from < 0 {
				from = 0
			}
			to := block.end + ctxLines + 1
			if to > len(fr.lines) {
				to = len(fr.lines)
			}

			if totalLines > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "----- /%s - lines from %d to %d -----\n", fr.relPath, from+1, to)
			for i := from; i < to; i++ {
				if totalLines >= 500 {
					cut = true
					break
				}
				b.WriteString(fr.lines[i])
				b.WriteByte('\n')
				totalLines++
			}
			if cut {
				break
			}
		}
		if cut {
			break
		}
	}

	text := strings.TrimRight(b.String(), "\n")
	if cut {
		text = "Output cut at 500 lines, refine the search pattern\n" + text
	}
	return mcp.NewToolResultText(text), nil
}
