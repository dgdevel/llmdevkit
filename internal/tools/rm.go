package tools

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
)

func RmHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	abs, err := Resolve(p)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if IsConfigPath(abs) || IsIgnored(abs) {
		return mcp.NewToolResultError("access denied"), nil
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return mcp.NewToolResultText("done"), nil
	}
	if err := os.RemoveAll(abs); err != nil {
		return mcp.NewToolResultError(MaskPath(err.Error())), nil
	}
	return mcp.NewToolResultText("done"), nil
}
