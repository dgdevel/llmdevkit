package tools

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
)

func MvHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	src, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dst, err := req.RequireString("dest")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	srcAbs, err := Resolve(src)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dstAbs, err := Resolve(dst)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if IsConfigPath(srcAbs) || IsIgnored(srcAbs) || IsConfigPath(dstAbs) || IsIgnored(dstAbs) {
		return mcp.NewToolResultError("access denied"), nil
	}
	if _, err := os.Stat(srcAbs); os.IsNotExist(err) {
		return mcp.NewToolResultError("source not found"), nil
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return mcp.NewToolResultError("destination already exists"), nil
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return mcp.NewToolResultError(MaskPath(err.Error())), nil
	}
	return mcp.NewToolResultText("done"), nil
}
