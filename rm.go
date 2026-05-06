package main

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
)

func rmHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
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
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return mcp.NewToolResultText("ok"), nil
	}
	if err := os.RemoveAll(abs); err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}
	return mcp.NewToolResultText("ok"), nil
}
