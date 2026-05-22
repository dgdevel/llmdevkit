package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"llmdevkit/internal/llms"
	"llmdevkit/internal/mcps"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// -- API: Agents -------------------------------------------------------------

type agentInfo struct {
	Name string `json:"name"`
	LLM  string `json:"llm"`
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	var list []agentInfo
	if s.agentCfg != nil {
		for _, a := range s.agentCfg.Agents {
			llmName := a.LLM
			if s.llmCfg != nil {
				if l, ok := s.llmCfg.Lookup(a.LLM); ok {
					llmName = l.Model
					if llmName == "" {
						llmName = l.Name
					}
				}
			}
			list = append(list, agentInfo{Name: a.Name, LLM: llmName})
		}
	}
	writeJSON(w, list)
}

// -- API: LLMs ---------------------------------------------------------------

type llmInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	ContextSize int    `json:"context_size,omitempty"`
}

func (s *Server) handleLLMs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	var list []llmInfo
	if s.llmCfg != nil {
		for _, l := range s.llmCfg.LLMs {
			displayName := l.Model
			if displayName == "" {
				displayName = l.Name
			}
			list = append(list, llmInfo{Name: l.Name, DisplayName: displayName, ContextSize: l.ContextSize})
		}
	}
	writeJSON(w, list)
}

// -- API: Tool Definitions ---------------------------------------------------

func (s *Server) handleToolDefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		writeJSON(w, []ToolDefInfo{})
		return
	}
	defs, err := s.resolveToolDefs(r.Context(), agentName)
	if err != nil {
		s.dlog.Log("handleToolDefs agent=%s error: %v", agentName, err)
		writeJSON(w, []ToolDefInfo{})
		return
	}
	writeJSON(w, defs)
}

func (s *Server) resolveToolDefs(ctx context.Context, agentName string) ([]ToolDefInfo, error) {
	agentCfg, _ := s.agentCfg.Lookup(agentName)
	if agentCfg == nil {
		return nil, fmt.Errorf("agent %q not found", agentName)
	}
	var defs []ToolDefInfo
	for _, token := range agentCfg.ToolNames() {
		switch token {
		case "devkit":
			// Prefer cached tool defs from ACP side channel.
			s.toolDefsMu.RLock()
			cached := s.toolDefsCache["devkit"]
			s.toolDefsMu.RUnlock()
			if len(cached) > 0 {
				defs = append(defs, cached...)
			} else {
				// Cache empty (ACP not started yet) -- probe llmdevkit mcp directly.
				d, err := s.resolveMCPToolDefsStdio(ctx, "llmdevkit", "mcp", s.rootDir)
				if err != nil {
					s.dlog.Log("resolveToolDefs devkit fallback: %v", err)
				} else {
					defs = append(defs, d...)
					// Cache for subsequent calls.
					s.toolDefsMu.Lock()
					s.toolDefsCache["devkit"] = d
					s.toolDefsMu.Unlock()
				}
			}
		case "agents":
			defs = append(defs,
				ToolDefInfo{Name: "agents_available", Description: "List available agents"},
				ToolDefInfo{Name: "agent_invoke", Description: "Invoke a sub-agent by name with a prompt"},
			)
		case "ask":
			defs = append(defs,
				ToolDefInfo{Name: "ask_open_ended", Description: "Ask user an open-ended question", Parameters: map[string]any{"type": "object", "properties": map[string]any{"question": map[string]any{"type": "string", "description": "The question text"}}}},
				ToolDefInfo{Name: "ask_exec", Description: "Ask user to execute a command", Parameters: map[string]any{"type": "object", "properties": map[string]any{"cmdline": map[string]any{"type": "string", "description": "Command line"}, "timeout": map[string]any{"type": "integer", "description": "Timeout in seconds"}}}},
				ToolDefInfo{Name: "ask_multiple_choice", Description: "Ask user a multiple choice question"},
				ToolDefInfo{Name: "rename_conversation", Description: "Rename the current conversation", Parameters: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "description": "New title for the conversation"}}, "required": []string{"title"}}},
			)
		default:
			if s.mcpCfg != nil {
				scfg, ok := s.mcpCfg.MCPS[token]
				if !ok {
					continue
				}
				var d []ToolDefInfo
				var err error
				if scfg.Stdio != "" {
					parts := parseStdioCommand(scfg.Stdio)
					d, err = s.resolveMCPToolDefsStdio(ctx, parts[0], parts[1:]...)
				} else if scfg.URL != "" {
					d, err = s.resolveMCPToolDefsURL(ctx, scfg.URL)
				}
				if err != nil {
					s.dlog.Log("resolveToolDefs %s: %v", token, err)
				} else {
					defs = append(defs, d...)
				}
			}
		}
	}
	return defs, nil
}

func (s *Server) resolveMCPToolDefsStdio(ctx context.Context, cmd string, args ...string) ([]ToolDefInfo, error) {
	c, err := client.NewStdioMCPClient(cmd, nil, args...)
	if err != nil {
		return nil, err
	}
	return s.initAndListTools(ctx, c)
}

func (s *Server) resolveMCPToolDefsURL(ctx context.Context, url string) ([]ToolDefInfo, error) {
	c, err := client.NewStreamableHttpClient(url)
	if err != nil {
		return nil, err
	}
	return s.initAndListTools(ctx, c)
}

func (s *Server) initAndListTools(ctx context.Context, c *client.Client) ([]ToolDefInfo, error) {
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "llmdevkit-server",
				Version: "0.1.0",
			},
		},
	}); err != nil {
		return nil, err
	}

	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}

	var defs []ToolDefInfo
	for _, t := range toolsResult.Tools {
		schema := map[string]any{}
		if b, jerr := json.Marshal(t.InputSchema); jerr == nil {
			json.Unmarshal(b, &schema)
		}
		defs = append(defs, ToolDefInfo{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}
	return defs, nil
}

// executeManualToolCalls executes a list of manual tool calls by invoking the
// appropriate MCP server. It resolves which MCP server provides each tool based
// on the agent's configuration, calls the tool, and returns the text results.
func (s *Server) executeManualToolCalls(ctx context.Context, agentName string, calls []ManualToolCall) ([]string, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	agentCfg, _ := s.agentCfg.Lookup(agentName)
	if agentCfg == nil {
		return nil, fmt.Errorf("agent %q not found", agentName)
	}

	// Build a map: toolName -> MCP server config (or "devkit"/"ask"/"agents")
	toolToServer := make(map[string]string)
	for _, token := range agentCfg.ToolNames() {
		switch token {
		case "devkit", "ask", "agents":
			// These are handled in-process by the ACP agent, not callable from server.
			// We can only call MCP stdio/url tools directly.
		default:
			if s.mcpCfg != nil {
				if scfg, ok := s.mcpCfg.MCPS[token]; ok {
					// Resolve tool defs for this MCP server and map each tool name
					var defs []ToolDefInfo
					var err error
					if scfg.Stdio != "" {
						parts := parseStdioCommand(scfg.Stdio)
						defs, err = s.resolveMCPToolDefsStdio(ctx, parts[0], parts[1:]...)
					} else if scfg.URL != "" {
						defs, err = s.resolveMCPToolDefsURL(ctx, scfg.URL)
					}
					if err != nil {
						s.dlog.Log("executeManualToolCalls resolve %s: %v", token, err)
						continue
					}
					for _, d := range defs {
						toolToServer[d.Name] = token
					}
				}
			}
		}
	}

	var results []string
	for _, tc := range calls {
		mcpToken, ok := toolToServer[tc.Name]
		if !ok {
			results = append(results, fmt.Sprintf("Tool %q not available for manual execution (only MCP tools are supported)", tc.Name))
			continue
		}

		scfg, ok := s.mcpCfg.MCPS[mcpToken]
		if !ok {
			results = append(results, fmt.Sprintf("MCP server %q not found for tool %q", mcpToken, tc.Name))
			continue
		}

		result, err := s.callMCPTool(ctx, scfg, tc.Name, tc.Arguments)
		if err != nil {
			results = append(results, fmt.Sprintf("Error calling %s: %v", tc.Name, err))
		} else {
			results = append(results, result)
		}
	}
	return results, nil
}

// callMCPTool connects to an MCP server and calls a tool by name.
func (s *Server) callMCPTool(ctx context.Context, scfg mcps.ServerConfig, toolName string, args map[string]any) (string, error) {
	var c *client.Client
	var err error
	var cleanup func()

	if scfg.Stdio != "" {
		parts := parseStdioCommand(scfg.Stdio)
		c, err = client.NewStdioMCPClient(parts[0], nil, parts[1:]...)
		if err != nil {
			return "", fmt.Errorf("stdio MCP client: %w", err)
		}
		cleanup = func() { c.Close() }
	} else if scfg.URL != "" {
		c, err = client.NewStreamableHttpClient(scfg.URL)
		if err != nil {
			return "", fmt.Errorf("http MCP client: %w", err)
		}
		cleanup = func() {} // http client doesn't need close
	} else {
		return "", fmt.Errorf("MCP server has no stdio or url config")
	}
	defer cleanup()

	// Initialize
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := c.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "llmdevkit-server",
				Version: "0.1.0",
			},
		},
	}); err != nil {
		return "", fmt.Errorf("MCP initialize: %w", err)
	}

	// Call tool
	callReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	result, err := c.CallTool(ctx, callReq)
	if err != nil {
		return "", fmt.Errorf("call tool: %w", err)
	}

	// Extract text from result
	var texts []string
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			texts = append(texts, textContent.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

// Suppress unused
var _ = llms.Config{}
