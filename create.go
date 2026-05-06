package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
)

func createHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
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
	if _, err := os.Stat(abs); err == nil {
		return mcp.NewToolResultError("file already exists"), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}
	return mcp.NewToolResultText("ok"), nil
}
