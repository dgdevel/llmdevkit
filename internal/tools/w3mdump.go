package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/go-shiori/go-readability"
	"github.com/mark3labs/mcp-go/mcp"
)

const W3mMaxBodySize = 5 << 20
const w3mMaxMarkdown = 200 << 10

var W3mHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	},
	Timeout: 25 * time.Second,
}

func W3mdumpHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURL, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid URL: %v", err)), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return mcp.NewToolResultError("only http and https URLs are supported"), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating request: %v", err)), nil
	}
	httpReq.Header.Set("User-Agent", "llmdevkit/0.1")

	resp, err := W3mHTTPClient.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching URL: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcp.NewToolResultError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)), nil
	}

	article, err := readability.FromReader(io.LimitReader(resp.Body, W3mMaxBodySize), parsed)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("parsing content: %v", err)), nil
	}

	var content string
	if strings.TrimSpace(article.Content) != "" {
		converter := md.NewConverter("", true, nil)
		markdown, err := converter.ConvertString(article.Content)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("converting to markdown: %v", err)), nil
		}
		content = markdown
	} else {
		content = article.TextContent
	}

	var buf strings.Builder
	if article.Title != "" {
		buf.WriteString("# ")
		buf.WriteString(article.Title)
		buf.WriteString("\n\n")
	}
	if content != "" {
		buf.WriteString(content)
	}

	result := buf.String()
	if len(result) > w3mMaxMarkdown {
		result = result[:w3mMaxMarkdown]
		if article.Title != "" {
			result = "# PAGE TOO LONG - PARTIAL OUTPUT\n\n" + result
		} else {
			result = "# PAGE TOO LONG - PARTIAL OUTPUT\n\n" + result
		}
	}

	return mcp.NewToolResultText(result), nil
}
