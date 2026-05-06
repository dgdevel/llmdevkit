package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func splitLines(data string) []string {
	lines := strings.Split(data, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func editHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	startLine := req.GetInt("start_line_number", 0)
	if startLine <= 0 {
		return mcp.NewToolResultError("start_line_number must be >= 1"), nil
	}
	originalWindow, err := req.RequireString("original_window")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	modifiedWindow, err := req.RequireString("modified_window")
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

	fileContent := string(data)
	originalLines := splitLines(originalWindow)

	var matches []int
	for i := 0; i <= len(splitLines(fileContent))-len(originalLines); i++ {
		if matchAt(fileContent, originalWindow, i) {
			matches = append(matches, i+1)
		}
	}

	switch len(matches) {
	case 0:
		return mcp.NewToolResultText("ko: no match"), nil
	case 1:
		actualLine := matches[0]
		if absDiff(actualLine, startLine) <= 5 {
			newContent := replaceAt(fileContent, originalWindow, modifiedWindow, actualLine-1)
			if err := os.WriteFile(abs, []byte(newContent), 0644); err != nil {
				return mcp.NewToolResultError(maskPath(err.Error())), nil
			}
			if actualLine != startLine {
				return mcp.NewToolResultText(fmt.Sprintf("done, start_line_number was wrong, it was %d instead", actualLine)), nil
			}
			return mcp.NewToolResultText("done"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("ko: no match (start_line_number %d too far from actual match at %d)", startLine, actualLine)), nil
	default:
		withinRange := 0
		for _, m := range matches {
			if absDiff(m, startLine) <= 5 {
				withinRange++
			}
		}
		if withinRange == 1 {
			var actualLine int
			for _, m := range matches {
				if absDiff(m, startLine) <= 5 {
					actualLine = m
					break
				}
			}
			newContent := replaceAt(fileContent, originalWindow, modifiedWindow, actualLine-1)
			if err := os.WriteFile(abs, []byte(newContent), 0644); err != nil {
				return mcp.NewToolResultError(maskPath(err.Error())), nil
			}
			if actualLine != startLine {
				return mcp.NewToolResultText(fmt.Sprintf("done, start_line_number was wrong, it was %d instead", actualLine)), nil
			}
			return mcp.NewToolResultText("done"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("ko: %d matches found, ensure start_line_number is right", len(matches))), nil
	}
}

func matchAt(fileContent, originalWindow string, lineIdx int) bool {
	lines := splitLines(fileContent)
	origLines := splitLines(originalWindow)
	if lineIdx+len(origLines) > len(lines) {
		return false
	}
	for i, ol := range origLines {
		if lines[lineIdx+i] != ol {
			return false
		}
	}
	return true
}

func replaceAt(fileContent, originalWindow, modifiedWindow string, lineIdx int) string {
	lines := splitLines(fileContent)
	origLines := splitLines(originalWindow)
	modLines := splitLines(modifiedWindow)

	replacement := make([]string, 0, len(lines)-len(origLines)+len(modLines))
	replacement = append(replacement, lines[:lineIdx]...)
	replacement = append(replacement, modLines...)
	replacement = append(replacement, lines[lineIdx+len(origLines):]...)

	result := strings.Join(replacement, "\n")
	if strings.HasSuffix(fileContent, "\n") || len(lines) > 0 {
		result += "\n"
	}
	return result
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
