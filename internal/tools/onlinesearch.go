package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/mark3labs/mcp-go/mcp"
)

func OnlineSearchHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("search_query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating request: %v", err)), nil
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; llmdevkit/0.1)")

	resp, err := W3mHTTPClient.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("searching: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcp.NewToolResultError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)), nil
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, W3mMaxBodySize))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("parsing response: %v", err)), nil
	}

	type searchResult struct {
		title       string
		url         string
		description string
	}

	var results []searchResult
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		link := s.Find("a.result__a")
		title := strings.TrimSpace(link.Text())
		href, _ := link.Attr("href")
		resultURL := extractDDGURL(href)

		snippet := strings.TrimSpace(s.Find("a.result__snippet").Text())

		if title != "" && resultURL != "" {
			results = append(results, searchResult{
				title:       title,
				url:         resultURL,
				description: snippet,
			})
		}
	})

	if len(results) == 0 {
		return mcp.NewToolResultText("No results found"), nil
	}

	var buf strings.Builder
	for i, r := range results {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("Title: ")
		buf.WriteString(r.title)
		buf.WriteString("\nUrl: ")
		buf.WriteString(r.url)
		buf.WriteString("\nDescription: ")
		buf.WriteString(r.description)
		buf.WriteString("\n")
	}

	return mcp.NewToolResultText(buf.String()), nil
}

func extractDDGURL(href string) string {
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if u.Host == "duckduckgo.com" || u.Host == "" {
		if u.Path == "/l/" {
			return u.Query().Get("uddg")
		}
	}
	return href
}
