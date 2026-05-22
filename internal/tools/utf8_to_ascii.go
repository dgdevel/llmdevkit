package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func Utf8ToAsciiHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var cEscaped strings.Builder
	var htmlEntity strings.Builder

	for _, r := range text {
		if r > 127 {
			fmt.Fprintf(&cEscaped, "\\u%04X", r)
			fmt.Fprintf(&htmlEntity, "&#x%04X;", r)
		} else {
			cEscaped.WriteRune(r)
			htmlEntity.WriteRune(r)
		}
	}

	var sb strings.Builder
	sb.WriteString("C escaped string: " + cEscaped.String() + "\n")
	sb.WriteString("HTML entity: " + htmlEntity.String())

	return mcp.NewToolResultText(sb.String()), nil
}
