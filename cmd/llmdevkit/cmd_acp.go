package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
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
	store      *acp.MemoryStore[*sessionData]

	client acp.Client // set after connection init

	// Cached devkit MCP client -- spawned once, reused across prompts.
	devkitMu       sync.Mutex
	devkitOnce     bool
	devkitTools    []devkitToolEntry
	_devkitClient  *client.Client
}

type devkitToolEntry struct {
	Name        string
	Description string
	InputSchema map[string]any
}

func runACP() {
	rootDir, _ := os.Getwd()
	rootDir, _ = filepath.Abs(rootDir)

	debuglog.Init(rootDir)
	dlog := debuglog.For("acp")
	dlog.Log("acp starting, rootDir=%s", rootDir)

	// Load configs
	llmCfg, err := llms.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit acp: load llms config: %v\n", err)
		os.Exit(1)
	}
	if llmCfg == nil {
		localPath := llms.ConfigPath(rootDir)
		globalPath := llms.GlobalConfigPath()
		fmt.Fprintf(os.Stderr, "llmdevkit acp: no llms.yml config found\n")
		fmt.Fprintf(os.Stderr, "  searched:\n")
		fmt.Fprintf(os.Stderr, "    %s\n", localPath)
		if globalPath != "" {
			fmt.Fprintf(os.Stderr, "    %s\n", globalPath)
		}
		os.Exit(1)
	}

	mcpCfg, err := mcps.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit acp: load mcps config: %v\n", err)
		os.Exit(1)
	}

	agentCfg, err := agents.LoadMergedConfig(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit acp: load agents config: %v\n", err)
		os.Exit(1)
	}

	agent := &llmdevkitAgent{
		llmCfg:   llmCfg,
		mcpCfg:   mcpCfg,
		agentCfg: agentCfg,
		rootDir:  rootDir,
	}

	store := acp.NewMemoryStore[*sessionData]()
	agent.store = store
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
		fmt.Fprintf(os.Stderr, "llmdevkit acp: %v\n", err)
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
	dlog := debuglog.For("acp")
	// Extract text from prompt content blocks
	var promptText string
	for _, block := range params.Prompt {
		if txt, ok := block.AsText(); ok {
			promptText += txt.Text
		}
	}

	dlog.Log("Prompt session=%s text_len=%d", params.SessionID, len(promptText))

	sideURL := os.Getenv("LLMDEVKIT_SIDE_CHANNEL")
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

	// Resolve LLM: use meta override if provided, else agent default
	llmName := agentCfg.LLM
	if params.Meta != nil {
		if v, ok := params.Meta["llm"].(string); ok && v != "" {
			llmName = v
		}
	}
	llmDef, ok := a.llmCfg.Lookup(llmName)
	if !ok {
		stream.SendText(ctx, fmt.Sprintf("Error: LLM %q not found in llms.yml\n", llmName))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	// Build tool registry
	registry, err := a.buildToolRegistry(ctx, agentCfg, sideURL)
	if err != nil {
		stream.SendText(ctx, fmt.Sprintf("Error building tools: %v\n", err))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	
	// Store cancel func so Cancel() can abort this prompt
	if sd, ok := a.store.Get(params.SessionID); ok {
		sd.CancelFn = cancel
	}
	defer func() {
		if sd, ok := a.store.Get(params.SessionID); ok {
			sd.CancelFn = nil
		}
	}()

	// Detect continuation: server signals via _meta that this is a resumed conversation
	isContinuation := false
	if params.Meta != nil {
		if v, ok := params.Meta["continuation"].(bool); ok && v {
			isContinuation = true
		}
	}
	
	// Build messages: prepend conversation history from session
	var messages []runner.ChatMessage
	if sd, ok := a.store.Get(params.SessionID); ok && len(sd.Messages) > 0 {
		messages = make([]runner.ChatMessage, len(sd.Messages), len(sd.Messages)+1)
		copy(messages, sd.Messages)
	}
	// On continuation after server restart, session store is empty;
	// restore history from the meta field injected by the server.
	if len(messages) == 0 && isContinuation {
		if raw, ok := params.Meta["history"]; ok {
			var restored []runner.ChatMessage
			if b, err := json.Marshal(raw); err == nil {
				if err := json.Unmarshal(b, &restored); err == nil {
					messages = restored
				}
			}
		}
	}
	messages = append(messages, runner.ChatMessage{Role: "user", Content: promptText})
	
	// Map LLM tool-call IDs -> ACP ToolCallIDs so start/complete/fail
	// use the same ID for each tool call.
	var tcIDMu sync.Mutex
	tcIDMap := make(map[string]acp.ToolCallID)
	
	runnerOpts := []runner.Option{
		runner.WithRootDir(a.rootDir),
		runner.WithTextCallback(func(text string) {
			stream.SendText(promptCtx, text)
		}),
		runner.WithToolStartCallback(func(id, title, kind string, arguments json.RawMessage) {
			acpTCID := acp.ToolCallID(fmt.Sprintf("tc_%d", toolCallCounter.Add(1)))
			tcIDMu.Lock()
			tcIDMap[id] = acpTCID
			tcIDMu.Unlock()
			stream.StartToolCall(promptCtx, acpTCID, title, acp.ToolKindExecute)
			// Send rawInput update so the tool_call event carries arguments
			if len(arguments) > 0 {
				statusInProgress := acp.ToolCallStatusInProgress
				stream.SendUpdate(promptCtx, acp.NewSessionUpdateToolCallUpdate(acp.ToolCallUpdate{
					ToolCallID: acpTCID,
					RawInput:   arguments,
					Status:     &statusInProgress,
				}))
			}
		}),
		runner.WithToolUpdateCallback(func(id, status, content string) {
			tcIDMu.Lock()
			acpTCID, ok := tcIDMap[id]
			tcIDMu.Unlock()
			if !ok {
				return
			}
			switch status {
			case "completed":
				displayContent := content
				if displayContent == "" {
					displayContent = "(no output)"
				}
				stream.CompleteToolCall(promptCtx, acpTCID, acp.NewToolCallContentContent(acp.NewContentBlockText(displayContent)))
			case "failed":
				stream.FailToolCall(promptCtx, acpTCID)
			}
		}),
		runner.WithTokenStatsCallback(func(stats runner.TokenStats) {
			if sideURL != "" {
				sideChannelNotify(promptCtx, sideURL, map[string]any{
					"type":              "token_stats",
					"prompt_tokens":     stats.PromptTokens,
					"completion_tokens": stats.CompletionTokens,
					"total_tokens":      stats.TotalTokens,
					"llm_calls":         stats.LLMCalls,
				})
			}
		}),
	}
	if isContinuation {
			runnerOpts = append(runnerOpts, runner.WithSkipConversationBegin())
			runnerOpts = append(runnerOpts, runner.WithSkipTurnBegin())
		}
	r := runner.NewRunner(llmDef, agentCfg, registry, a.agentCfg, runnerOpts...)

	fullMessages, result, err := r.RunPrompt(promptCtx, messages, promptText)
	if err != nil {
		stream.SendText(promptCtx, fmt.Sprintf("Error: %v\n", err))
		return &acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	}

	// Save conversation history to session store
	if sd, ok := a.store.Get(params.SessionID); ok {
		sd.Messages = fullMessages
		a.store.Set(params.SessionID, sd)
	}

	_ = result
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *llmdevkitAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	if sd, ok := a.store.Get(params.SessionID); ok {
		if sd.CancelFn != nil {
			sd.CancelFn()
		}
	}
	return nil
}

// buildToolRegistry creates the tool registry for a given agent configuration.
func (a *llmdevkitAgent) buildToolRegistry(ctx context.Context, agentCfg *agents.AgentConfig, sideURL string) (*runner.ToolRegistry, error) {
	registry := runner.NewToolRegistry()

	if agentCfg.Tools == "" {
		return registry, nil
	}

	toolTokens := agentCfg.ToolNames()

	for _, token := range toolTokens {
		switch token {
		case "devkit":
			// Register devkit tools (in-process llmdevkit mcp server)
			if err := a.registerDevkitTools(ctx, registry, sideURL); err != nil {
				return nil, fmt.Errorf("devkit tools: %w", err)
			}

		case "agents":
			// Register agents_available and agent_invoke special tools
			a.registerAgentTools(registry)

		case "ask":
			// Register ask tools that use side-channel to llmdevkit server
			a.registerAskTools(registry)

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

// registerDevkitTools spawns llmdevkit mcp once (cached), registers its tools,
// and sends tool definitions to llmdevkit server via sideURL.
func (a *llmdevkitAgent) registerDevkitTools(ctx context.Context, registry *runner.ToolRegistry, sideURL string) error {
	a.devkitMu.Lock()
	defer a.devkitMu.Unlock()

	if !a.devkitOnce {
		// Build args for llmdevkit mcp
		args := []string{"mcp", a.rootDir}
		if os.Getenv("LLMDEVKIT_ENABLE_INDEXER") == "1" {
			args = []string{"mcp", "-enable-indexer", a.rootDir}
		}

		execName := "llmdevkit"
		c, err := client.NewStdioMCPClient(execName, nil, args...)
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
			schema := map[string]any{}
			if b, err := json.Marshal(tool.InputSchema); err == nil {
				json.Unmarshal(b, &schema)
			}
			desc := tool.Description
			if desc == "" {
				desc = tool.Name
			}
			a.devkitTools = append(a.devkitTools, devkitToolEntry{
				Name:        tool.Name,
				Description: desc,
				InputSchema: schema,
			})
		}

		// Store client for tool calls
		a._devkitClient = c

		// Send tool definitions to llmdevkit server on first spawn.
		if sideURL != "" && len(a.devkitTools) > 0 {
			var toolDefs []map[string]any
			for _, t := range a.devkitTools {
				toolDefs = append(toolDefs, map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				})
			}
			sideChannelNotify(ctx, sideURL, map[string]any{
				"type":  "tool_defs",
				"tools": toolDefs,
			})
		}

		a.devkitOnce = true
	}

	// Register all cached tools using the shared client
	c := a._devkitClient
	for _, entry := range a.devkitTools {
		e := entry
		registry.Add(&runner.ToolDef{
			Name:        e.Name,
			Description: e.Description,
			InputSchema: e.InputSchema,
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				req := mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name:      e.Name,
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

// registerAskTools registers ask_open_ended, ask_exec, ask_multiple_choice
// These use a side-channel HTTP call to the llmdevkit server UI.
func (a *llmdevkitAgent) registerAskTools(registry *runner.ToolRegistry) {
	sideURL := os.Getenv("LLMDEVKIT_SIDE_CHANNEL")

	registry.Add(&runner.ToolDef{
		Name:        "ask_open_ended",
		Description: "Ask the user an open-ended question and wait for their response",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to ask the user"},
			},
			"required": []string{"question"},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			question, _ := args["question"].(string)
			if sideURL == "" {
				return question, nil
			}
			return sideChannelCall(ctx, sideURL, map[string]any{
				"type":     "ask_open_ended",
				"question": question,
			})
		},
	})

	registry.Add(&runner.ToolDef{
		Name:        "ask_exec",
		Description: "Ask the user to authorize command execution. User can modify command, change timeout, approve or deny.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmdline": map[string]any{"type": "string", "description": "The command line to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds", "default": 30},
			},
			"required": []string{"cmdline"},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			cmdline, _ := args["cmdline"].(string)
			timeout := 30
			if t, ok := args["timeout"].(float64); ok {
				timeout = int(t)
			}
			if sideURL == "" {
				return fmt.Sprintf("No side-channel: would execute: %s (timeout: %ds)", cmdline, timeout), nil
			}
			return sideChannelCall(ctx, sideURL, map[string]any{
				"type":    "ask_exec",
				"cmdline": cmdline,
				"timeout": timeout,
			})
		},
	})

	registry.Add(&runner.ToolDef{
		Name:        "ask_multiple_choice",
		Description: "Ask the user a multiple choice question",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question":        map[string]any{"type": "string", "description": "The question to ask"},
				"choices":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of choices"},
				"allow_open_ended": map[string]any{"type": "boolean", "description": "Whether to allow a custom text response", "default": false},
			},
			"required": []string{"question", "choices"},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			question, _ := args["question"].(string)
			var choices []string
			if c, ok := args["choices"].([]interface{}); ok {
				for _, v := range c {
					choices = append(choices, fmt.Sprint(v))
				}
			}
			allowOpen := false
			if a, ok := args["allow_open_ended"].(bool); ok {
				allowOpen = a
			}
			if sideURL == "" {
				return question, nil
			}
			return sideChannelCall(ctx, sideURL, map[string]any{
				"type":             "ask_multiple_choice",
				"question":         question,
				"choices":          choices,
				"allow_open_ended": allowOpen,
			})
		},
	})

	registry.Add(&runner.ToolDef{
		Name:        "rename_conversation",
		Description: "Rename the current conversation with a new title",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string", "description": "New title for the conversation"},
			},
			"required": []string{"title"},
		},
		Call: func(ctx context.Context, args map[string]any) (string, error) {
			title, _ := args["title"].(string)
			if sideURL == "" {
				return "No side-channel available", nil
			}
			return sideChannelCall(ctx, sideURL, map[string]any{
				"type":  "rename_conversation",
				"title": title,
			})
		},
	})
}

// sideChannelCall POSTs to the llmdevkit server side-channel and waits for user response.
func sideChannelCall(ctx context.Context, sideURL string, payload map[string]any) (string, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", sideURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("side-channel call: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("side-channel HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return string(respBody), nil
}

// sideChannelNotify sends a fire-and-forget POST to the side-channel (no response needed).
func sideChannelNotify(ctx context.Context, sideURL string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", sideURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
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

			subRegistry, err := a.buildToolRegistry(ctx, subCfg, "")
			if err != nil {
				return "", fmt.Errorf("build tools for agent %q: %w", agentName, err)
			}

			messages := []runner.ChatMessage{
				{Role: "user", Content: prompt},
			}

			r := runner.NewRunner(llmDef, subCfg, subRegistry, a.agentCfg, runner.WithRootDir(a.rootDir))
			_, result, err := r.RunPrompt(ctx, messages, prompt)
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
