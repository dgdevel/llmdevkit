package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/llms"
	"llmdevkit/internal/runner"
	"llmdevkit/internal/mcps"

	acp "github.com/ironpark/go-acp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// sessionData holds per-session state.
type sessionData struct {
	Cwd      string
	CancelFn context.CancelFunc
	Messages []runner.ChatMessage // use exported name
}

// exported ChatMessage type alias for runner package access
type ChatMessage = runner.ChatMessage

type llmdevkitAgent struct {
	llmCfg     *llms.Config
	mcpCfg     *mcps.Config
	agentCfg   *agents.Config
	rootDir    string

	client acp.Client // set after connection init
}

func main() {
	rootDir, _ := os.Getwd()
	rootDir, _ = filepath.Abs(rootDir)

	// Load configs
	llmCfg, err := llms.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit-acp: load llms config: %v\n", err)
		os.Exit(1)
	}
	if llmCfg == nil {
		fmt.Fprintf(os.Stderr, "llmdevkit-acp: no llms.yml config found in %s\n", rootDir)
		os.Exit(1)
	}

	mcpCfg, err := mcps.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit-acp: load mcps config: %v\n", err)
		os.Exit(1)
	}

	agentCfg, err := agents.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit-acp: load agents config: %v\n", err)
		os.Exit(1)
	}

	agent := &llmdevkitAgent{
		llmCfg:   llmCfg,
		mcpCfg:   mcpCfg,
		agentCfg: agentCfg,
		rootDir:  rootDir,
	}

	store := acp.NewMemoryStore[*sessionData]()
	factory := func(ctx context.Context, params *acp.NewSessionRequest) (acp.SessionID, *sessionData, error) {
		id := acp.GenerateSessionID()
		cwd := params.Cwd
		if cwd == "" {
			cwd = rootDir
		}
		return id, &sessionData{Cwd: cwd}, nil
	}

	conn := acp.NewAgentSideConnection(agent, os.Stdin, os.Stdout,
		acp.WithSessionStore(store, factory),
		acp.WithMiddleware(acp.RecoveryMiddleware()),
		acp.WithMiddleware(acp.LoggingMiddleware(log.Printf)),
	)

	agent.client = conn.Client()

	if err := conn.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit-acp: %v\n", err)
		os.Exit(1)
	}
}

// Initialize handles the ACP initialize method.
func (a *llmdevkitAgent) Initialize(ctx context.Context, params *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{
		ProtocolVersion: 1,
		AgentInfo: &acp.Implementation{
			Name:    "llmdevkit-acp",
			Title:   "LLM DevKit Agent",
			Version: "0.1.0",
		},
		AgentCapabilities: &acp.AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &acp.PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			MCPCapabilities: &acp.MCPCapabilities{
				HTTP: true,
				SSE:  true,
			},
		},
		AuthMethods: []acp.AuthMethod{},
	}, nil
}

func (a *llmdevkitAgent) Authenticate(ctx context.Context, params *acp.AuthenticateRequest) (*acp.AuthenticateResponse, error) {
	return &acp.AuthenticateResponse{}, nil
}

func (a *llmdevkitAgent) SetSessionMode(ctx context.Context, params *acp.SetSessionModeRequest) (*acp.SetSessionModeResponse, error) {
	return &acp.SetSessionModeResponse{}, nil
}

func (a *llmdevkitAgent) SetSessionConfigOption(ctx context.Context, params *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	return &acp.SetSessionConfigOptionResponse{}, nil
}

var toolCallCounter atomic.Uint64

// Prompt handles the ACP prompt method.
func (a *llmdevkitAgent) Prompt(ctx context.Context, params *acp.PromptRequest) (*acp.PromptResponse, error) {
	// Extract text from prompt content blocks
	var promptText string
	for _, block := range params.Prompt {
		if txt, ok := block.AsText(); ok {
			promptText += txt.Text
		}
	}

	stream := acp.NewSessionStream(a.client, params.SessionID)

	// Select agent config: use default or first available
	var agentCfg *agents.AgentConfig
	if a.agentCfg != nil {
		agentCfg = a.agentCfg.Default()
	}
	if agentCfg == nil {
		stream.SendText(ctx, "Error: no agent configured in agents.yml\n")
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	// Look up LLM config
	llmDef, ok := a.llmCfg.Lookup(agentCfg.LLM)
	if !ok {
		stream.SendText(ctx, fmt.Sprintf("Error: LLM %q not found in llms.yml\n", agentCfg.LLM))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	// Build tool registry
	registry, err := a.buildToolRegistry(ctx, agentCfg)
	if err != nil {
		stream.SendText(ctx, fmt.Sprintf("Error building tools: %v\n", err))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Build messages
	messages := []runner.ChatMessage{
		{Role: "user", Content: promptText},
	}

	r := runner.NewRunner(llmDef, agentCfg, registry, a.agentCfg,
		runner.WithTextCallback(func(text string) {
			stream.SendText(promptCtx, text)
		}),
		runner.WithToolStartCallback(func(id, title, kind string) {
			tcID := acp.ToolCallID(fmt.Sprintf("tc_%d", toolCallCounter.Add(1)))
			stream.StartToolCall(promptCtx, tcID, title, acp.ToolKindExecute)
		}),
		runner.WithToolUpdateCallback(func(id, status, content string) {
			tcID := acp.ToolCallID(fmt.Sprintf("tc_%d", toolCallCounter.Add(1)))
			switch status {
			case "completed":
				stream.CompleteToolCall(promptCtx, tcID, acp.NewToolCallContentContent(acp.NewContentBlockText(content)))
			case "failed":
				stream.FailToolCall(promptCtx, tcID)
			}
		}),
	)

	result, err := r.RunPrompt(promptCtx, messages, promptText)
	if err != nil {
		stream.SendText(promptCtx, fmt.Sprintf("Error: %v\n", err))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	_ = result
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *llmdevkitAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	// Context cancellation is handled by the prompt context
	return nil
}

// buildToolRegistry creates the tool registry for a given agent configuration.
func (a *llmdevkitAgent) buildToolRegistry(ctx context.Context, agentCfg *agents.AgentConfig) (*runner.ToolRegistry, error) {
	registry := runner.NewToolRegistry()

	if agentCfg.Tools == "" {
		return registry, nil
	}

	toolTokens := agentCfg.ToolNames()

	for _, token := range toolTokens {
		switch token {
		case "devkit":
			// Register devkit tools (in-process llmdevkit-mcp server)
			if err := a.registerDevkitTools(ctx, registry); err != nil {
				return nil, fmt.Errorf("devkit tools: %w", err)
			}

		case "agents":
			// Register agents_available and agent_invoke special tools
			a.registerAgentTools(registry)

		default:
			// Look up in mcps config
			if a.mcpCfg != nil {
				scfg, ok := a.mcpCfg.MCPS[token]
				if !ok {
					return nil, fmt.Errorf("tool source %q not found in mcps.yml", token)
				}
				if err := a.registerMCPTools(ctx, registry, token, scfg); err != nil {
					return nil, fmt.Errorf("mcp tools %s: %w", token, err)
				}
			} else {
				return nil, fmt.Errorf("tool source %q not found (no mcps.yml)", token)
			}
		}
	}

	return registry, nil
}

// registerDevkitTools connects to an in-process llmdevkit-mcp server and registers its tools.
func (a *llmdevkitAgent) registerDevkitTools(ctx context.Context, registry *runner.ToolRegistry) error {
	// Start llmdevkit-mcp as a subprocess
	execName := "llmdevkit-mcp"
	c, err := client.NewStdioMCPClient(execName, nil, a.rootDir)
	if err != nil {
		return fmt.Errorf("start devkit: %w", err)
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "llmdevkit-acp",
				Version: "0.1.0",
			},
		},
	}); err != nil {
		return fmt.Errorf("init devkit: %w", err)
	}

	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list devkit tools: %w", err)
	}

	for _, tool := range toolsResult.Tools {
		t := tool
		schema := map[string]any{}
		if b, err := json.Marshal(t.InputSchema); err == nil {
			json.Unmarshal(b, &schema)
		}
		desc := t.Description
		if desc == "" {
			desc = t.Name
		}
		registry.Add(&runner.ToolDef{
			Name:        t.Name,
			Description: desc,
			InputSchema: schema,
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				req := mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name:      t.Name,
						Arguments: args,
					},
				}
				result, err := c.CallTool(ctx, req)
				if err != nil {
					return "", err
				}
				return toolResultToString(result), nil
			},
		})
	}
	return nil
}

// registerMCPTools connects to an MCP server from config and registers its tools.
func (a *llmdevkitAgent) registerMCPTools(ctx context.Context, registry *runner.ToolRegistry, name string, scfg mcps.ServerConfig) error {
	var c *client.Client
	var err error

	switch {
	case scfg.URL != "":
		c, err = client.NewStreamableHttpClient(scfg.URL)
		if err != nil {
			return err
		}
		if err := c.Start(ctx); err != nil {
			return err
		}
	case scfg.SSE != "":
		c, err = client.NewSSEMCPClient(scfg.SSE)
		if err != nil {
			return err
		}
		if err := c.Start(ctx); err != nil {
			return err
		}
	case scfg.Stdio != "":
		parts := parseStdioCommand(scfg.Stdio)
		c, err = client.NewStdioMCPClient(parts[0], nil, parts[1:]...)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("no transport for MCP server %s", name)
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "llmdevkit-acp",
				Version: "0.1.0",
			},
		},
	}); err != nil {
		return err
	}

	toolsResult, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return err
	}

	// If tools filter specified, only register those. Otherwise register all with prefix.
	filterTools := scfg.Tools != nil && len(scfg.Tools) > 0

	for _, tool := range toolsResult.Tools {
		t := tool
		tcfg, inFilter := scfg.Tools[t.Name]

		if filterTools && !inFilter {
			continue
		}

		toolName := t.Name
		if !inFilter && scfg.Prefix != "" {
			toolName = scfg.Prefix + t.Name
		}
		if inFilter && tcfg.Rename != "" {
			toolName = tcfg.Rename
		}

		desc := t.Description
		if inFilter && tcfg.Description != "" {
			desc = tcfg.Description
		}
		if desc == "" {
			desc = toolName
		}

		schema := map[string]any{}
		if b, err := json.Marshal(t.InputSchema); err == nil {
			json.Unmarshal(b, &schema)
		}

		upstreamName := t.Name
		registry.Add(&runner.ToolDef{
			Name:        toolName,
			Description: desc,
			InputSchema: schema,
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				req := mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name:      upstreamName,
						Arguments: args,
					},
				}
				result, err := c.CallTool(ctx, req)
				if err != nil {
					return "", err
				}
				return toolResultToString(result), nil
			},
		})
	}
	return nil
}

// registerAgentTools registers the agents_available and agent_invoke special tools.
func (a *llmdevkitAgent) registerAgentTools(registry *runner.ToolRegistry) {
	registry.Add(&runner.ToolDef{
		Name:        "agents_available",
		Description: "List all available agents by name",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			if a.agentCfg == nil {
				return "[]", nil
			}
			names := make([]string, 0, len(a.agentCfg.Agents))
			for _, ag := range a.agentCfg.Agents {
				names = append(names, ag.Name)
			}
			b, _ := json.Marshal(names)
			return string(b), nil
		},
	})

	registry.Add(&runner.ToolDef{
		Name:        "agent_invoke",
		Description: "Invoke a sub-agent by name with a prompt. The sub-agent runs with a fresh context.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_name": map[string]any{
					"type":        "string",
					"description": "Name of the agent to invoke",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to send to the sub-agent",
				},
			},
			"required": []string{"agent_name", "prompt"},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			agentName, _ := args["agent_name"].(string)
			prompt, _ := args["prompt"].(string)
			if agentName == "" || prompt == "" {
				return "", fmt.Errorf("agent_name and prompt are required")
			}

			subCfg, ok := a.agentCfg.Lookup(agentName)
			if !ok {
				return "", fmt.Errorf("agent %q not found", agentName)
			}

			llmDef, ok := a.llmCfg.Lookup(subCfg.LLM)
			if !ok {
				return "", fmt.Errorf("LLM %q not found for agent %q", subCfg.LLM, agentName)
			}

			subRegistry, err := a.buildToolRegistry(ctx, subCfg)
			if err != nil {
				return "", fmt.Errorf("build tools for agent %q: %w", agentName, err)
			}

			messages := []runner.ChatMessage{
				{Role: "user", Content: prompt},
			}

			r := runner.NewRunner(llmDef, subCfg, subRegistry, a.agentCfg)
			result, err := r.RunPrompt(ctx, messages, prompt)
			if err != nil {
				return "", fmt.Errorf("agent %q error: %w", agentName, err)
			}
			return result, nil
		},
	})
}

func toolResultToString(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		if t, ok := c.(mcp.TextContent); ok {
			sb.WriteString(t.Text)
		} else {
			b, _ := json.Marshal(c)
			sb.Write(b)
		}
	}
	if result.IsError {
		return "Error: " + sb.String()
	}
	return sb.String()
}

// parseStdioCommand splits a stdio command string into command and args,
// respecting quoted segments.
func parseStdioCommand(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false

	for _, r := range s {
		switch {
		case r == '"' || r == '\'':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// Suppress unused import
var _ = time.Sleep
