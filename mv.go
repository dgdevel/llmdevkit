package main

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
)

func mvHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	src, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dst, err := req.RequireString("dest")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	srcAbs, err := resolve(src)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dstAbs, err := resolve(dst)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if isConfigPath(srcAbs) || isIgnored(srcAbs) || isConfigPath(dstAbs) || isIgnored(dstAbs) {
		return mcp.NewToolResultError("access denied"), nil
	}
	if _, err := os.Stat(srcAbs); os.IsNotExist(err) {
		return mcp.NewToolResultError("source not found"), nil
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return mcp.NewToolResultError("destination already exists"), nil
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return mcp.NewToolResultError(maskPath(err.Error())), nil
	}
	return mcp.NewToolResultText("ok"), nil
}
