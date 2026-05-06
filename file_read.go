package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"llmdevkit/internal/cfg"

	"github.com/mark3labs/mcp-go/mcp"
)

func fileReadHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	lineRange, err := req.RequireString("line_range")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	abs, err := resolve(p)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if isConfigPath(abs) || isIgnored(abs) {
		return mcp.NewToolResultError("access denied"), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}

	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	if isBinary(data[:checkLen]) {
		return mcp.NewToolResultError("binary file, use stat for file info"), nil
	}

	blockSize := 100
	config := cfg.MergedRead(rootDir)
	if core, ok := config["core"]; ok {
		blockSize = cfg.Atoi(core["file_read_block_size"], 100)
	}
	if blockSize < 1 {
		blockSize = 1
	}

	lines := strings.Split(string(data), "\n")
	from, to, warn := parseLineRange(lineRange, len(lines))
	if strings.HasPrefix(warn, "error:") {
		return mcp.NewToolResultError(warn), nil
	}
	if from >= len(lines) {
		return mcp.NewToolResultText(warn), nil
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from >= to {
		return mcp.NewToolResultText(warn), nil
	}

	selected := lines[from:to]
	var b strings.Builder
	if warn != "" {
		fmt.Fprintf(&b, "%s\n", warn)
	}
	for i := 0; i < len(selected); i += blockSize {
		end := i + blockSize
		if end > len(selected) {
			end = len(selected)
		}
		fmt.Fprintf(&b, "----- %s - lines from %d to %d -----\n", p, from+i+1, from+end)
		for _, line := range selected[i:end] {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if to < len(lines) {
		fmt.Fprintf(&b, "----- %s - remaining lines from %d to %d -----\n", p, to+1, len(lines))
	} else {
		fmt.Fprintf(&b, "----- %s - EOF -----\n", p)
	}
	return mcp.NewToolResultText(b.String()), nil
}
