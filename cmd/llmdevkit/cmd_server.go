package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
	"llmdevkit/internal/llms"
	"llmdevkit/internal/mcps"
	"llmdevkit/internal/tools"

	acp "github.com/ironpark/go-acp"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

//go:embed ui.html js sw.js
var staticFS embed.FS

// ── Data types ──────────────────────────────────────────────────────────────

type BubbleMessage struct {
	Type           string   `json:"type"`
	Content        string   `json:"content"`
	Name           string   `json:"name,omitempty"`
	ID             string   `json:"id,omitempty"`
	Timestamp      string   `json:"timestamp,omitempty"`
	Cmdline        string   `json:"cmdline,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	Choices        []string `json:"choices,omitempty"`
	AllowOpenEnded bool     `json:"allow_open_ended,omitempty"`
	Question       string   `json:"question,omitempty"`
	Answered       bool     `json:"answered,omitempty"`
	Approved       bool     `json:"approved,omitempty"`
	Answer         string   `json:"answer,omitempty"`
	TokenCount     int      `json:"token_count,omitempty"`
}

type TokenStats struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	LLMCalls         int `json:"llm_calls"`
}

type ManualToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolDefInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type Conversation struct {
	ID           string          `json:"id"`
	Agent        string          `json:"agent"`
	SystemPrompt string          `json:"system_prompt,omitempty"`
	Tools        []string        `json:"tools,omitempty"`
	ToolDefs     []ToolDefInfo   `json:"tool_defs,omitempty"`
	Title        string          `json:"title,omitempty"`
	Messages     []BubbleMessage `json:"messages"`
	Running      bool            `json:"running"`
	FileSize     int64           `json:"file_size,omitempty"`
	Queue        []string        `json:"queue,omitempty"`

	ACPSessionID string `json:"acp_session_id,omitempty"`
	Initialized  bool   `json:"-"`

	PendingTokenCount int `json:"-"` // set by token_stats side channel, applied when prompt finishes
}

type jsonlLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ── Server state ────────────────────────────────────────────────────────────

type Server struct {
	rootDir  string
	llmCfg   *llms.Config
	agentCfg *agents.Config
	mcpCfg   *mcps.Config
	dlog     *debuglog.Logger
	mu       sync.RWMutex
	convs    map[string]*Conversation
	convOrder []string

	acpConn      *acp.ClientSideConnection
	acpCmd       *exec.Cmd
	acpMu        sync.RWMutex
	acpConnected bool

	askMu    sync.Mutex
	askPends map[string]chan *AskAnswer

	sseMu      sync.RWMutex
	sseClients map[chan SSEEvent]struct{}

	toolDefsMu    sync.RWMutex
	toolDefsCache map[string][]ToolDefInfo // agent name → cached tool defs from ACP

	enableIndexer bool

	notifMu    sync.Mutex
	notifBuf   []notifEvent
}

type notifEvent struct {
	Ts     float64 `json:"ts"`
	Event  string  `json:"event"`
	ConvID string  `json:"conv_id"`
	Title  string  `json:"title,omitempty"`
}

type SSEEvent struct {
	ConversationID string          `json:"conversation_id"`
	Event          string          `json:"event"`
	Data          json.RawMessage  `json:"data"`
}

type AskAnswer struct {
	Type       string `json:"type"`
	Answer     string `json:"answer,omitempty"`
	Approved   bool   `json:"approved,omitempty"`
	Cmdline    string `json:"cmdline,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	DenyReason string `json:"deny_reason,omitempty"`
}

// pushNotif adds a notification event to the ring buffer.
func (s *Server) pushNotif(event, convID, title string) {
	s.notifMu.Lock()
	s.notifBuf = append(s.notifBuf, notifEvent{
		Ts:     float64(time.Now().UnixMilli()) / 1000,
		Event:  event,
		ConvID: convID,
		Title:  title,
	})
	// Keep last 100 entries
	if len(s.notifBuf) > 100 {
		s.notifBuf = s.notifBuf[len(s.notifBuf)-100:]
	}
	s.notifMu.Unlock()
}

// handleNotifications returns notification events since a given timestamp.
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	sinceStr := r.URL.Query().Get("since")
	var since float64
	if sinceStr != "" {
		since, _ = strconv.ParseFloat(sinceStr, 64)
	}
	s.notifMu.Lock()
	var result []notifEvent
	for _, n := range s.notifBuf {
		if n.Ts > since {
			result = append(result, n)
		}
	}
	s.notifMu.Unlock()
	if result == nil {
		result = []notifEvent{}
	}
	writeJSON(w, result)
}

// ── Main ────────────────────────────────────────────────────────────────────

func runServer() {
	enableIndexer := flag.Bool("enable-indexer", false, "pass --enable-indexer through to llmdevkit mcp via ACP")
	flag.Parse()

	rootDir, _ := os.Getwd()
	rootDir, _ = filepath.Abs(rootDir)

	tools.RootDir = rootDir

	debuglog.Init(rootDir)
	dlog := debuglog.For("server")
	dlog.Log("server starting, rootDir=%s", rootDir)

	llmCfg, err := llms.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load llms config: %v", err)
	}

	agentCfg, err := agents.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load agents config: %v", err)
	}

	mcpCfg, err := mcps.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load mcps config: %v", err)
	}

	srv := &Server{
		rootDir:       rootDir,
		llmCfg:        llmCfg,
		agentCfg:      agentCfg,
		mcpCfg:        mcpCfg,
		dlog:          dlog,
		convs:         make(map[string]*Conversation),
		askPends:      make(map[string]chan *AskAnswer),
		sseClients:    make(map[chan SSEEvent]struct{}),
		toolDefsCache: make(map[string][]ToolDefInfo),
		enableIndexer: *enableIndexer,
	}

	if err := srv.loadConversations(); err != nil {
		log.Printf("warning: load conversations: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveUI)
	mux.HandleFunc("/api/agents", srv.handleAgents)
	mux.HandleFunc("/api/tooldefs", srv.handleToolDefs)
	mux.HandleFunc("/api/conversations", srv.handleConversations)
	mux.HandleFunc("/api/conversations/", srv.handleConversationActions)
	mux.HandleFunc("/api/ask/", srv.handleAskAnswer)
	mux.HandleFunc("/api/tasks/delete", srv.handleTaskDelete)
	mux.HandleFunc("/api/tasks/clear", srv.handleTasksClear)
	mux.HandleFunc("/api/tasks", srv.handleTasksRead)
	mux.HandleFunc("/api/sidechannel", srv.handleSideChannel)
	mux.HandleFunc("/api/events", srv.handleSSE)
	mux.HandleFunc("/api/notifications", srv.handleNotifications)

	addr := ":18681"

	httpServer := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on SIGINT/SIGTERM.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("shutting down...")
		httpServer.Close()
		debuglog.Close()
	}()

	log.Printf("llmdevkit server listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// ── Static UI ───────────────────────────────────────────────────────────────

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		data, _ := staticFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
		return
	}
	// Serve JS module files from embedded fs
	if strings.HasPrefix(path, "/js/") && strings.HasSuffix(path, ".js") {
		data, err := staticFS.ReadFile(path[1:]) // strip leading /
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Write(data)
		return
	}
	// Serve service worker
	if path == "/sw.js" {
		data, err := staticFS.ReadFile("sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(data)
		return
	}
	http.NotFound(w, r)
}

// Close kills the ACP subprocess if running.
func (s *Server) Close() {
	s.acpMu.Lock()
	cmd := s.acpCmd
	s.acpMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// ── API: Agents ─────────────────────────────────────────────────────────────

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

// ── API: Tool Definitions ───────────────────────────────────────────────────

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
				// Cache empty (ACP not started yet) — probe llmdevkit mcp directly.
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

	// Build a map: toolName → MCP server config (or "devkit"/"ask"/"agents")
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

// ── API: Conversations ──────────────────────────────────────────────────────

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		list := make([]*Conversation, 0)
		for _, id := range s.convOrder {
			if c, ok := s.convs[id]; ok {
				// Refresh file size
				if fi, err := os.Stat(s.convFile(c.ID)); err == nil {
					c.FileSize = fi.Size()
				}
				list = append(list, c)
			}
		}
		s.mu.RUnlock()
		writeJSON(w, list)

	case http.MethodPost:
		var req struct {
			Agent        string `json:"agent"`
			SystemPrompt string `json:"system_prompt"`
			Title        string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		title := req.Title
		if title == "" {
			title = time.Now().Format("2006-01-02T15:04:05")
		}
		conv := &Conversation{
			ID:           fmt.Sprintf("conv_%d", time.Now().UnixNano()),
			Agent:        req.Agent,
			SystemPrompt: req.SystemPrompt,
			Messages:     []BubbleMessage{},
			Title:        title,
		}
		s.dlog.Log("POST /api/conversations agent=%s conv_id=%s", req.Agent, conv.ID)
		s.mu.Lock()
		s.convs[conv.ID] = conv
		s.convOrder = append([]string{conv.ID}, s.convOrder...)
		s.mu.Unlock()
		s.appendJSONL(conv.ID, "conversation_created", conv)
		if fi, err := os.Stat(s.convFile(conv.ID)); err == nil {
			conv.FileSize = fi.Size()
		}
		s.broadcastSSE("", "conversation_created", conv)
		writeJSON(w, conv)

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleConversationActions(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/conversations/"), "/")
	convID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.mu.RLock()
			conv, ok := s.convs[convID]
			s.mu.RUnlock()
			if !ok {
				http.Error(w, "not found", 404)
				return
			}
			writeJSON(w, conv)
		case http.MethodDelete:
			s.mu.Lock()
			delete(s.convs, convID)
			var newOrder []string
			for _, id := range s.convOrder {
				if id != convID {
					newOrder = append(newOrder, id)
				}
			}
			s.convOrder = newOrder
			s.mu.Unlock()
			os.Remove(s.convFile(convID))
			s.broadcastSSE("", "conversation_deleted", map[string]string{"id": convID})
			w.WriteHeader(204)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}

	action := parts[1]
	switch action {
	case "init":
		s.handleConvInit(w, r, convID)
	case "prompt":
		s.handleConvPrompt(w, r, convID)
	case "cancel":
		s.handleConvCancel(w, r, convID)
	case "rename":
		s.handleConvRename(w, r, convID)
	case "undo":
		s.handleConvUndo(w, r, convID)
	case "trim":
		s.handleConvTrim(w, r, convID)
	case "enqueue":
		s.handleConvEnqueue(w, r, convID)
	case "queue":
		s.handleConvQueueList(w, r, convID)
	default:
		http.Error(w, "unknown action", 404)
	}
}

func (s *Server) handleConvInit(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Prompt    string           `json:"prompt"`
		ToolCalls []ManualToolCall `json:"tool_calls,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.dlog.Log("INIT conv=%s prompt=%q tool_calls=%d", convID, req.Prompt, len(req.ToolCalls))

	s.mu.Lock()
	conv, ok := s.convs[convID]
	s.mu.Unlock()
	if !ok {
		s.dlog.Log("INIT conv=%s NOT FOUND", convID)
		http.Error(w, "conversation not found", 404)
		return
	}

	if err := s.ensureACPConnection(); err != nil {
		s.dlog.Log("INIT ensureACPConnection FAILED: %v", err)
		writeJSONError(w, fmt.Sprintf("ACP init: %v", err))
		return
	}
	s.dlog.Log("INIT ACP connected, creating session...")

	s.acpMu.Lock()
	sessResp, err := s.acpConn.NewSession(r.Context(), &acp.NewSessionRequest{
		Cwd: s.rootDir,
	})
	s.acpMu.Unlock()
	if err != nil {
		s.dlog.Log("INIT NewSession FAILED: %v", err)
		writeJSONError(w, fmt.Sprintf("new session: %v", err))
		return
	}

	conv.ACPSessionID = string(sessResp.SessionID)
	conv.Initialized = true
	s.dlog.Log("INIT session created: acp_session=%s agent=%s", conv.ACPSessionID, conv.Agent)

	var agentCfg *agents.AgentConfig
	if s.agentCfg != nil {
		agentCfg, _ = s.agentCfg.Lookup(conv.Agent)
	}
	if agentCfg != nil {
		sysPrompt := conv.SystemPrompt
		if sysPrompt == "" {
			sysPrompt = agentCfg.SystemPrompt
		}
		// Append AGENTS.md content if present (same as runner does)
		if s.rootDir != "" {
			agentsMD, err := os.ReadFile(filepath.Join(s.rootDir, "AGENTS.md"))
			if err == nil && len(agentsMD) > 0 {
				sysPrompt += "\n\n" + string(agentsMD)
			}
		}
		conv.SystemPrompt = sysPrompt
		conv.Tools = agentCfg.ToolNames()
	}

	// Execute manual tool calls
	var toolResults []string
	if len(req.ToolCalls) > 0 {
		results, err := s.executeManualToolCalls(r.Context(), conv.Agent, req.ToolCalls)
		if err != nil {
			writeJSONError(w, fmt.Sprintf("tool execution error: %v", err))
			return
		}
		toolResults = results
	}

	promptText := req.Prompt
	if len(toolResults) > 0 {
		promptText = req.Prompt + "\n\n--- Manual Tool Results ---\n" + strings.Join(toolResults, "\n\n")
	}

	conv.Messages = append(conv.Messages, BubbleMessage{
		Type:      "user",
		Content:   req.Prompt,
		Timestamp: nowISO(),
	})

	// Add tool_request/tool_response bubbles for manual tool calls
	for i, tc := range req.ToolCalls {
		toolReqContent := map[string]any{
			"title":     tc.Name,
			"name":      tc.Name,
			"arguments": tc.Arguments,
		}
		toolReqJSON, _ := json.Marshal(toolReqContent)
		s.addBubble(convID, BubbleMessage{Type: "tool_request", Name: tc.Name, Content: string(toolReqJSON), Timestamp: nowISO()})
		if i < len(toolResults) {
			s.addBubble(convID, BubbleMessage{Type: "tool_response", Name: tc.Name, Content: toolResults[i], Timestamp: nowISO()})
		}
	}

	s.appendJSONL(convID, "init", map[string]interface{}{
		"agent":        conv.Agent,
		"acp_session":  conv.ACPSessionID,
		"system_prompt": conv.SystemPrompt,
		"tools":         conv.Tools,
	})
	s.appendJSONL(convID, "bubble", BubbleMessage{Type: "user", Content: req.Prompt, Timestamp: nowISO()})
	s.setConvRunning(convID, true)
	// Snapshot conv for SSE broadcast — goroutine may modify conv concurrently
	s.mu.Lock()
	convCopy := *conv
	convCopy.Messages = make([]BubbleMessage, len(conv.Messages))
	copy(convCopy.Messages, conv.Messages)
	s.mu.Unlock()
	s.broadcastSSE("", "conversation_updated", &convCopy)

	s.dlog.Log("INIT starting runACPPrompt goroutine for conv=%s", convID)
	go s.runACPPrompt(convID, promptText)

	writeJSON(w, map[string]interface{}{
		"conversation": conv,
	})
}

func (s *Server) handleConvPrompt(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Prompt    string           `json:"prompt"`
		ToolCalls []ManualToolCall `json:"tool_calls,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.mu.RLock()
	conv, ok := s.convs[convID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "conversation not found", 404)
		return
	}

	if !conv.Initialized || conv.ACPSessionID == "" {
		http.Error(w, "session not initialized", 400)
		return
	}

	s.mu.RLock()
	if conv.Running {
		s.mu.RUnlock()
		http.Error(w, "conversation is running, use enqueue instead", 409)
		return
	}
	s.mu.RUnlock()

	// Execute manual tool calls and collect results
	var toolResults []string
	if len(req.ToolCalls) > 0 {
		results, err := s.executeManualToolCalls(r.Context(), conv.Agent, req.ToolCalls)
		if err != nil {
			writeJSONError(w, fmt.Sprintf("tool execution error: %v", err))
			return
		}
		toolResults = results
	}

	promptText := req.Prompt
	if len(toolResults) > 0 {
		promptText = req.Prompt + "\n\n--- Manual Tool Results ---\n" + strings.Join(toolResults, "\n\n")
	}

	s.mu.Lock()
	ts := nowISO()
	conv.Messages = append(conv.Messages, BubbleMessage{Type: "user", Content: req.Prompt, Timestamp: ts})
	// Add tool_request and tool_response bubbles for each manual tool call
	for i, tc := range req.ToolCalls {
		toolReqContent := map[string]any{
			"title":     tc.Name,
			"name":      tc.Name,
			"arguments": tc.Arguments,
		}
		toolReqJSON, _ := json.Marshal(toolReqContent)
		s.mu.Unlock()
		s.addBubble(convID, BubbleMessage{Type: "tool_request", Name: tc.Name, Content: string(toolReqJSON), Timestamp: nowISO()})
		if i < len(toolResults) {
			s.addBubble(convID, BubbleMessage{Type: "tool_response", Name: tc.Name, Content: toolResults[i], Timestamp: nowISO()})
		}
		s.mu.Lock()
	}
	// Snapshot for SSE broadcast under lock to avoid data race with goroutine
	convCopy := *conv
	convCopy.Messages = make([]BubbleMessage, len(conv.Messages))
	copy(convCopy.Messages, conv.Messages)
	s.mu.Unlock()

	s.appendJSONL(convID, "bubble", BubbleMessage{Type: "user", Content: req.Prompt, Timestamp: ts})
	s.setConvRunning(convID, true)
	s.broadcastSSE("", "conversation_updated", &convCopy)

	go s.runACPPrompt(convID, promptText)

	writeJSON(w, map[string]interface{}{})
}

func (s *Server) handleConvCancel(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.mu.RLock()
	conv, ok := s.convs[convID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	if conv.ACPSessionID != "" && s.acpConn != nil {
		s.acpMu.RLock()
		conn := s.acpConn
		s.acpMu.RUnlock()
		conn.Cancel(r.Context(), &acp.CancelNotification{
			SessionID: acp.SessionID(conv.ACPSessionID),
		})
	}
	s.setConvRunning(convID, false)
	w.WriteHeader(204)
}

func (s *Server) handleConvRename(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if ok && req.Title != "" {
		conv.Title = req.Title
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	s.appendJSONL(convID, "conversation_created", conv)
	s.broadcastSSE("", "conversation_updated", conv)
	writeJSON(w, map[string]string{"title": conv.Title})
}

func (s *Server) handleConvUndo(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "not found", 404)
		return
	}
	if conv.Running {
		s.mu.Unlock()
		http.Error(w, "conversation is running", 400)
		return
	}

	// Find last user message index
	lastUserIdx := -1
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Type == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		s.mu.Unlock()
		http.Error(w, "no user message to undo", 400)
		return
	}

	// Cancel ACP session if active
	if conv.ACPSessionID != "" {
		s.acpMu.RLock()
		conn := s.acpConn
		s.acpMu.RUnlock()
		if conn != nil {
			conn.Cancel(context.Background(), &acp.CancelNotification{
				SessionID: acp.SessionID(conv.ACPSessionID),
			})
		}
		conv.ACPSessionID = ""
		conv.Initialized = false
	}

	// Truncate messages
	conv.Messages = conv.Messages[:lastUserIdx]
	convCopy := *conv
	convCopy.Messages = make([]BubbleMessage, len(conv.Messages))
	copy(convCopy.Messages, conv.Messages)
	s.mu.Unlock()

	// Rewrite JSONL from scratch
	s.rewriteJSONL(convID, conv)

	s.broadcastSSE("", "conversation_updated", &convCopy)
	writeJSON(w, conv)
}

func (s *Server) handleConvTrim(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "not found", 404)
		return
	}
	if conv.Running {
		s.mu.Unlock()
		http.Error(w, "conversation is running", 400)
		return
	}

	// Filter out tool_request and tool_response messages
	filtered := make([]BubbleMessage, 0, len(conv.Messages))
	for _, m := range conv.Messages {
		if m.Type == "tool_request" || m.Type == "tool_response" {
			continue
		}
		filtered = append(filtered, m)
	}

	if len(filtered) == len(conv.Messages) {
		s.mu.Unlock()
		http.Error(w, "no tool messages to trim", 400)
		return
	}

	conv.Messages = filtered
	convCopy := *conv
	convCopy.Messages = make([]BubbleMessage, len(conv.Messages))
	copy(convCopy.Messages, conv.Messages)
	s.mu.Unlock()

	s.rewriteJSONL(convID, conv)

	s.broadcastSSE("", "conversation_updated", &convCopy)
	writeJSON(w, conv)
}

func (s *Server) rewriteJSONL(convID string, conv *Conversation) {
	f, err := os.Create(s.convFile(convID))
	if err != nil {
		log.Printf("rewrite jsonl %s: %v", convID, err)
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	// conversation_created
	enc.Encode(jsonlLine{Type: "conversation_created", Payload: mustMarshal(conv)})

	if conv.Initialized {
		initPayload, _ := json.Marshal(map[string]interface{}{
			"agent":         conv.Agent,
			"acp_session":   conv.ACPSessionID,
			"system_prompt": conv.SystemPrompt,
			"tools":         conv.Tools,
		})
		enc.Encode(jsonlLine{Type: "init", Payload: initPayload})
	}

	for _, m := range conv.Messages {
		enc.Encode(jsonlLine{Type: "bubble", Payload: mustMarshal(&m)})
	}
	f.Sync()
	if fi, err := f.Stat(); err == nil {
		conv.FileSize = fi.Size()
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) handleConvEnqueue(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "empty prompt", 400)
		return
	}

	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "not found", 404)
		return
	}
	conv.Queue = append(conv.Queue, req.Prompt)
	queueCopy := make([]string, len(conv.Queue))
	copy(queueCopy, conv.Queue)
	s.mu.Unlock()

	s.broadcastSSE(convID, "queue_update", queueCopy)
	writeJSON(w, map[string]interface{}{"queue": queueCopy})
}

func (s *Server) handleConvQueueDelete(w http.ResponseWriter, r *http.Request, convID string, idx int) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "not found", 404)
		return
	}
	if idx < 0 || idx >= len(conv.Queue) {
		s.mu.Unlock()
		http.Error(w, "index out of range", 400)
		return
	}
	conv.Queue = append(conv.Queue[:idx], conv.Queue[idx+1:]...)
	queueCopy := make([]string, len(conv.Queue))
	copy(queueCopy, conv.Queue)
	s.mu.Unlock()

	s.broadcastSSE(convID, "queue_update", queueCopy)
	writeJSON(w, map[string]interface{}{"queue": queueCopy})
}

func (s *Server) handleConvQueueList(w http.ResponseWriter, r *http.Request, convID string) {
	s.mu.RLock()
	conv, ok := s.convs[convID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}

	// Check if there's a "delete" sub-action: /api/conversations/{id}/queue/{idx}
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/conversations/"+convID+"/queue/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "invalid index", 400)
			return
		}
		s.handleConvQueueDelete(w, r, convID, idx)
		return
	}

	s.mu.RLock()
	var queue []string
	if conv != nil {
		queue = make([]string, len(conv.Queue))
		copy(queue, conv.Queue)
	}
	s.mu.RUnlock()
	writeJSON(w, map[string]interface{}{"queue": queue})
}

// dequeuePrompt removes and returns the first queued prompt for a conversation.
// Returns empty string if queue is empty.
func (s *Server) dequeuePrompt(convID string) string {
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok || len(conv.Queue) == 0 {
		s.mu.Unlock()
		return ""
	}
	prompt := conv.Queue[0]
	conv.Queue = conv.Queue[1:]
	queueCopy := make([]string, len(conv.Queue))
	copy(queueCopy, conv.Queue)
	s.mu.Unlock()

	s.broadcastSSE(convID, "queue_update", queueCopy)
	return prompt
}

// ── ACP subprocess management ───────────────────────────────────────────────

func (s *Server) ensureACPConnection() error {
	s.acpMu.Lock()
	defer s.acpMu.Unlock()

	if s.acpConnected {
		s.dlog.Log("ensureACPConnection already connected")
		return nil
	}

	binPath, err := exec.LookPath("llmdevkit")
	if err != nil {
		return fmt.Errorf("llmdevkit not found in PATH: %w", err)
	}

	s.dlog.Log("ensureACPConnection spawning llmdevkit acp at %s", binPath)
	cmd := exec.Command(binPath, "acp")
	cmd.Dir = s.rootDir
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "LLMDEVKIT_SIDE_CHANNEL=http://localhost:18681/api/sidechannel")
	if s.enableIndexer {
		cmd.Env = append(cmd.Env, "LLMDEVKIT_ENABLE_INDEXER=1")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llmdevkit acp: %w", err)
	}

	s.acpCmd = cmd


	// Perform JSON-RPC initialize directly over the pipes before
	// handing them to the library.  This avoids the race where
	// Connection.SendRequest nil-derefs c.ctx because conn.Start()
	// (which sets c.ctx) runs in a goroutine.
	initReq := struct {
		Jsonrpc string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientCapabilities": map[string]any{},
			"clientInfo": map[string]any{
				"name":    "llmdevkit-server",
				"version": "0.1.0",
			},
			"protocolVersion": float64(1),
		},
	}
	initReqBytes, _ := json.Marshal(initReq)
	s.dlog.Log("ensureACPConnection sending raw initialize RPC...")
	if _, err := fmt.Fprintf(stdin, "%s\n", initReqBytes); err != nil {
		return fmt.Errorf("write initialize request: %w", err)
	}

	// Read the JSON-RPC response line from stdout byte-by-byte
	// to avoid buffering data that the library's readLoop needs.
	var respBuf bytes.Buffer
	buf := make([]byte, 1)
	for {
		if _, err := io.ReadFull(stdout, buf); err != nil {
			return fmt.Errorf("read initialize response: %w", err)
		}
		if buf[0] == '\n' {
			break
		}
		respBuf.Write(buf)
	}
	initRespBytes := respBuf.Bytes()
	s.dlog.Log("ensureACPConnection initialize response: %s", string(initRespBytes))

	var initResp struct {
		Jsonrpc string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(initRespBytes, &initResp); err != nil {
		return fmt.Errorf("parse initialize response: %w", err)
	}
	if initResp.Error != nil {
		return fmt.Errorf("initialize error: %d %s", initResp.Error.Code, initResp.Error.Message)
	}

	// Now hand the pipes to the library.  Start() will set c.ctx
	// internally, and all subsequent RPCs (NewSession, Prompt, etc.)
	// will work correctly.
	clientImpl := &acpClientHandler{server: s}
	conn := acp.NewClientSideConnection(clientImpl, stdin, stdout)
	s.acpConn = conn

	// Pre-set the inner Connection.ctx via reflect+unsafe so that
	// SendRequest won't nil-dereference c.ctx if called before
	// conn.Start()'s goroutine has a chance to run.  Start() will
	// overwrite this with a derived context.
	setContextConn(conn, context.Background())

	ctx := context.Background()
	go func() {
		if err := conn.Start(ctx); err != nil {
			s.dlog.Log("ACP connection readLoop exited: %v", err)
		}
	}()

	s.acpConnected = true
	s.dlog.Log("ensureACPConnection ACP connected and initialized")
	return nil
}

// ── ACP Client implementation ───────────────────────────────────────────────

// setContextConn pre-sets the ctx field on the inner *acp.Connection
// so that SendRequest won't nil-dereference before conn.Start() runs.
// conn.Start() will overwrite ctx with a derived context.
func setContextConn(csc *acp.ClientSideConnection, ctx context.Context) {
	// Walk: ClientSideConnection.conn (*Connection) -> Connection.ctx
	cscVal := reflect.ValueOf(csc).Elem()
	connField := cscVal.FieldByName("conn") // unexported *Connection
	connVal := connField.Elem()             // unexported Connection value
	ctxField := connVal.FieldByName("ctx")  // unexported context.Context

	// Write to unexported field via unsafe
	ctxPtr := unsafe.Pointer(ctxField.UnsafeAddr())
	*(*context.Context)(ctxPtr) = ctx
}

type acpClientHandler struct {
	server *Server
}

func (c *acpClientHandler) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	sid := string(params.SessionID)
	c.server.dlog.Log("SessionUpdate callback sid=%s", sid)
	c.server.mu.RLock()
	var convID string
	for _, conv := range c.server.convs {
		if conv.ACPSessionID == sid {
			convID = conv.ID
			break
		}
	}
	c.server.mu.RUnlock()

	if convID == "" {
		c.server.dlog.Log("SessionUpdate callback sid=%s no matching conv (convs=%d)", sid, len(c.server.convs))
		return nil
	}

	c.handleSessionUpdate(convID, params.Update)
	return nil
}

func (c *acpClientHandler) handleSessionUpdate(convID string, u acp.SessionUpdate) {
	raw, _ := json.Marshal(u)
	c.server.dlog.Log("SessionUpdate conv=%s raw=%s", convID, string(raw))
	if chunk, ok := u.AsAgentMessageChunk(); ok {
		if txt, ok2 := chunk.Content.AsText(); ok2 && txt.Text != "" {
			c.server.addBubble(convID, BubbleMessage{Type: "llm", Content: txt.Text})
		}
	}
	if chunk, ok := u.AsAgentThoughtChunk(); ok {
		if txt, ok2 := chunk.Content.AsText(); ok2 && txt.Text != "" {
			c.server.addBubble(convID, BubbleMessage{Type: "thinking", Content: txt.Text})
		}
	}
	if tc, ok := u.AsToolCall(); ok {
		raw, _ := json.Marshal(tc)
		c.server.addBubble(convID, BubbleMessage{Type: "tool_request", Name: tc.Title, Content: string(raw)})
	}
	if tcu, ok := u.AsToolCallUpdate(); ok {
		status := ""
		if tcu.Status != nil {
			status = string(*tcu.Status)
		}
		// Handle rawInput update — update the tool_request bubble with arguments
		if len(tcu.RawInput) > 0 {
			c.server.updateToolRequestRawInput(convID, string(tcu.ToolCallID), tcu.RawInput)
		}
		if status == "completed" || status == "failed" {
			var texts []string
			for _, ct := range tcu.Content {
				if cc, ok2 := ct.AsContent(); ok2 {
					if txt, ok3 := cc.Content.Content.AsText(); ok3 && txt.Text != "" {
						texts = append(texts, txt.Text)
					}
				}
			}
			content := strings.Join(texts, "\n")
			if content == "" {
				content = status
			}
			toolName := tcu.Title
			if toolName == "" {
				toolName = string(tcu.ToolCallID)
			}
			c.server.addBubble(convID, BubbleMessage{Type: "tool_response", Name: toolName, ID: string(tcu.ToolCallID), Content: content})
		}
	}
}

func (c *acpClientHandler) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	return &acp.RequestPermissionResponse{}, nil
}

func (c *acpClientHandler) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return nil, err
	}
	return &acp.ReadTextFileResponse{Content: string(data)}, nil
}

func (c *acpClientHandler) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	if err := os.WriteFile(params.Path, []byte(params.Content), 0644); err != nil {
		return nil, err
	}
	return &acp.WriteTextFileResponse{}, nil
}

func (c *acpClientHandler) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	return nil, fmt.Errorf("terminal not supported")
}
func (c *acpClientHandler) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	return nil, fmt.Errorf("terminal not supported")
}
func (c *acpClientHandler) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	return &acp.ReleaseTerminalResponse{}, nil
}
func (c *acpClientHandler) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	return nil, fmt.Errorf("terminal not supported")
}
func (c *acpClientHandler) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	return &acp.KillTerminalResponse{}, nil
}

// ── ACP prompt execution ────────────────────────────────────────────────────

func (s *Server) runACPPrompt(convID string, promptText string) {
	s.mu.RLock()
	conv, ok := s.convs[convID]
	s.mu.RUnlock()
	if !ok {
		s.dlog.Log("runACPPrompt conv=%s NOT FOUND", convID)
		return
	}

	s.dlog.Log("runACPPrompt conv=%s acp_session=%s prompt_len=%d", convID, conv.ACPSessionID, len(promptText))

	// Ensure ACP connection is alive (may have been lost on server restart)
	justConnected := false
	s.acpMu.Lock()
	if s.acpConn == nil || !s.acpConnected {
		justConnected = true
	}
	s.acpMu.Unlock()
	if justConnected {
		if err := s.ensureACPConnection(); err != nil {
			s.dlog.Log("runACPPrompt conv=%s ensureACPConnection FAILED: %v", convID, err)
			s.addBubble(convID, BubbleMessage{Type: "error", Content: fmt.Sprintf("ACP not connected: %v", err)})
			s.setConvRunning(convID, false)
			return
		}
		// Fresh ACP process — need a new session
		s.acpMu.Lock()
		sessResp, err := s.acpConn.NewSession(context.Background(), &acp.NewSessionRequest{
			Cwd: s.rootDir,
		})
		s.acpMu.Unlock()
		if err != nil {
			s.dlog.Log("runACPPrompt conv=%s NewSession FAILED: %v", convID, err)
			s.addBubble(convID, BubbleMessage{Type: "error", Content: fmt.Sprintf("ACP new session: %v", err)})
			s.setConvRunning(convID, false)
			return
		}
		s.mu.Lock()
		conv.ACPSessionID = string(sessResp.SessionID)
		conv.Initialized = true
		s.mu.Unlock()
		s.dlog.Log("runACPPrompt conv=%s new session created: %s", convID, conv.ACPSessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// Mark continuation if we reconnected and conversation has prior messages
	isContinuation := justConnected && len(conv.Messages) > 1

	contentBlock := acp.NewContentBlockText(promptText)
	promptReq := &acp.PromptRequest{
		SessionID: acp.SessionID(conv.ACPSessionID),
		Prompt:    []acp.ContentBlock{contentBlock},
	}
	if isContinuation {
		promptReq.Meta = map[string]any{"continuation": true}
	}

	s.dlog.Log("runACPPrompt conv=%s sending Prompt RPC...", convID)
	s.acpMu.RLock()
	conn := s.acpConn
	s.acpMu.RUnlock()
	resp, err := conn.Prompt(ctx, promptReq)

	if err != nil {
		errMsg := err.Error()
		s.dlog.Log("runACPPrompt conv=%s Prompt RPC ERROR: %v", convID, err)
		// If context deadline exceeded, the agent may still be running via async updates.
		// Log as warning, don't block with error bubble.
		if ctx.Err() == context.DeadlineExceeded || strings.Contains(errMsg, "deadline exceeded") {
			s.addBubble(convID, BubbleMessage{Type: "error", Content: fmt.Sprintf("Warning: prompt RPC timed out (2h). Agent may still be working: %v", err)})
		} else {
			s.addBubble(convID, BubbleMessage{Type: "error", Content: fmt.Sprintf("ACP prompt error: %v", err)})
		}
	} else {
		s.dlog.Log("runACPPrompt conv=%s Prompt RPC done, stop_reason=%s", convID, resp.StopReason)
	}

	if resp != nil {
		s.appendJSONL(convID, "prompt_response", map[string]string{
			"stop_reason": string(resp.StopReason),
		})
	}

	// Apply any pending token count to the last llm message.
	// Token stats may arrive before the llm content (via side channel vs ACP stream).
	s.mu.Lock()
	var finalTokenCount int
	if conv, ok := s.convs[convID]; ok && conv.PendingTokenCount > 0 {
		finalTokenCount = conv.PendingTokenCount
		for i := len(conv.Messages) - 1; i >= 0; i-- {
			if conv.Messages[i].Type == "llm" {
				conv.Messages[i].TokenCount = finalTokenCount
				break
			}
		}
		conv.PendingTokenCount = 0
	}
	s.mu.Unlock()
	if finalTokenCount > 0 {
		s.broadcastSSE(convID, "token_stats", TokenStats{TotalTokens: finalTokenCount})
	}

	s.setConvRunning(convID, false)

	// Dequeue next prompt if available
	if next := s.dequeuePrompt(convID); next != "" {
		s.mu.RLock()
		c, ok := s.convs[convID]
		s.mu.RUnlock()
		if ok {
			ts := nowISO()
			s.mu.Lock()
			c.Messages = append(c.Messages, BubbleMessage{Type: "user", Content: next, Timestamp: ts})
			convCopy := *c
			convCopy.Messages = make([]BubbleMessage, len(c.Messages))
			copy(convCopy.Messages, c.Messages)
			s.mu.Unlock()
			s.appendJSONL(convID, "bubble", BubbleMessage{Type: "user", Content: next, Timestamp: ts})
			s.setConvRunning(convID, true)
			s.broadcastSSE("", "conversation_updated", &convCopy)
			go s.runACPPrompt(convID, next)
		}
	}
}

// ── Bubble management ───────────────────────────────────────────────────────

// nowISO returns current time in ISO 8601 format.
func nowISO() string {
	return time.Now().Format(time.RFC3339)
}

func (s *Server) addBubble(convID string, b BubbleMessage) {
	if b.Timestamp == "" {
		b.Timestamp = nowISO()
	}
	s.dlog.Log("addBubble conv=%s type=%s content_len=%d", convID, b.Type, len(b.Content))
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		return
	}

	// Merge consecutive same-type for streaming
	if b.Type == "llm" || b.Type == "thinking" {
		if len(conv.Messages) > 0 {
			last := &conv.Messages[len(conv.Messages)-1]
			if last.Type == b.Type {
				last.Content += b.Content
				s.mu.Unlock()
				s.broadcastSSE(convID, "session_update", b)
				s.appendJSONL(convID, "bubble_merge", b)
				return
			}
		}
	}

	// Consume pending token count for new llm messages
	if b.Type == "llm" && conv.PendingTokenCount > 0 {
		b.TokenCount = conv.PendingTokenCount
		conv.PendingTokenCount = 0
	}

	conv.Messages = append(conv.Messages, b)
	s.mu.Unlock()

	s.broadcastSSE(convID, "session_update", b)
	s.appendJSONL(convID, "bubble", b)
}

// setConvRunning updates the running state for a conversation and broadcasts it.
func (s *Server) setConvRunning(convID string, isRunning bool) {
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if ok {
		conv.Running = isRunning
	}
	s.mu.Unlock()
	s.broadcastSSE(convID, "state", map[string]bool{"running": isRunning})
	if !isRunning {
		s.pushNotif("done", convID, s.convTitle(convID))
	}
}

// convTitle returns the conversation title or a fallback.
func (s *Server) convTitle(convID string) string {
	s.mu.RLock()
	conv, ok := s.convs[convID]
	s.mu.RUnlock()
	if ok && conv.Title != "" {
		return conv.Title
	}
	return convID
}

// updateToolRequestRawInput finds the last tool_request bubble matching the
// toolCallID and injects the rawInput (arguments) into its content JSON.
func (s *Server) updateToolRequestRawInput(convID, toolCallID string, rawInput json.RawMessage) {
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if !ok {
		s.mu.Unlock()
		return
	}
	// Find the tool_request bubble for this toolCallID by scanning backwards
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		m := &conv.Messages[i]
		if m.Type == "tool_request" {
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(m.Content), &parsed); err == nil {
				if tcID, ok := parsed["toolCallId"]; ok {
					var idStr string
					json.Unmarshal(tcID, &idStr)
					if idStr == toolCallID {
						// Inject rawInput
						parsed["rawInput"] = rawInput
						updated, _ := json.Marshal(parsed)
						m.Content = string(updated)
						s.mu.Unlock()
						s.broadcastSSE(convID, "tool_request_update", map[string]string{
							"toolCallId": toolCallID,
							"rawInput":   string(rawInput),
						})
						s.appendJSONL(convID, "tool_request_rawinput", map[string]string{
							"toolCallId": toolCallID,
							"rawInput":   string(rawInput),
						})
						return
					}
				}
			}
		}
	}
	s.mu.Unlock()
}

// ── Ask tool handling ───────────────────────────────────────────────────────

// ── Side-channel for ask tools (called by llmdevkit acp subprocess) ──────────

func (s *Server) handleSideChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	var askType string
	if t, ok := payload["type"]; ok {
		json.Unmarshal(t, &askType)
	}
	s.dlog.Log("handleSideChannel askType=%s", askType)

	// Find the conversation for this side-channel request.
	// Prefer a running conversation (most recently started), fallback to first initialized.
	s.mu.RLock()
	var convID string
	for _, id := range s.convOrder {
		if c, ok := s.convs[id]; ok && c.Running {
			convID = c.ID
			break
		}
	}
	if convID == "" {
		for _, id := range s.convOrder {
			if c, ok := s.convs[id]; ok && c.Initialized {
				convID = c.ID
				break
			}
		}
	}
	s.mu.RUnlock()

	if convID == "" {
		http.Error(w, "no active conversation", 400)
		return
	}

	// Handle token_stats (fire-and-forget, no user interaction needed)
	if askType == "token_stats" {
		var stats struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			LLMCalls         int `json:"llm_calls"`
		}
		if pt, ok := payload["prompt_tokens"]; ok {
			json.Unmarshal(pt, &stats.PromptTokens)
		}
		if ct, ok := payload["completion_tokens"]; ok {
			json.Unmarshal(ct, &stats.CompletionTokens)
		}
		if tt, ok := payload["total_tokens"]; ok {
			json.Unmarshal(tt, &stats.TotalTokens)
		}
		if lc, ok := payload["llm_calls"]; ok {
			json.Unmarshal(lc, &stats.LLMCalls)
		}

		s.mu.Lock()
		if conv, ok := s.convs[convID]; ok {
			conv.PendingTokenCount = stats.TotalTokens
		}
		s.mu.Unlock()

		s.appendJSONL(convID, "token_stats", stats)
		w.WriteHeader(204)
		return
	}

	// Handle tool_defs (fire-and-forget, ACP sends its tool registry)
	if askType == "tool_defs" {
		var toolDefs []ToolDefInfo
		if td, ok := payload["tools"]; ok {
			json.Unmarshal(td, &toolDefs)
		}
		if len(toolDefs) > 0 {
			s.toolDefsMu.Lock()
			s.toolDefsCache["devkit"] = toolDefs
			s.toolDefsMu.Unlock()
			s.dlog.Log("handleSideChannel cached %d devkit tool defs", len(toolDefs))
		}
		w.WriteHeader(204)
		return
	}

	// Handle rename_conversation (fire-and-forget from LLM tool)
	if askType == "rename_conversation" {
		var title string
		json.Unmarshal(payload["title"], &title)
		if title != "" {
			s.mu.Lock()
			if conv, ok := s.convs[convID]; ok {
				conv.Title = title
			}
			s.mu.Unlock()
			s.appendJSONL(convID, "conversation_created", s.convs[convID])
			s.broadcastSSE("", "conversation_updated", s.convs[convID])
		}
		w.WriteHeader(204)
		return
	}

	askID := fmt.Sprintf("ask_%d", time.Now().UnixNano())

	// Inject ask_id into the payload so the SSE broadcast carries it.
	{
		askIDJSON, _ := json.Marshal(askID)
		payload["ask_id"] = askIDJSON
	}

	// Add bubble for the ask
	var bubble BubbleMessage
	bubble.ID = askID

	switch askType {
	case "ask_open_ended":
		var question string
		json.Unmarshal(payload["question"], &question)
		bubble.Type = "ask_open_ended"
		bubble.Question = question

	case "ask_exec":
		var cmdline string
		json.Unmarshal(payload["cmdline"], &cmdline)
		var timeout int
		if t, ok := payload["timeout"]; ok {
			json.Unmarshal(t, &timeout)
		}
		if timeout == 0 {
			timeout = 30
		}
		bubble.Type = "ask_exec"
		bubble.Cmdline = cmdline
		bubble.Timeout = timeout

	case "ask_multiple_choice":
		var question string
		json.Unmarshal(payload["question"], &question)
		var choices []string
		json.Unmarshal(payload["choices"], &choices)
		var allowOpen bool
		json.Unmarshal(payload["allow_open_ended"], &allowOpen)
		bubble.Type = "ask_multiple_choice"
		bubble.Question = question
		bubble.Choices = choices
		bubble.AllowOpenEnded = allowOpen
	}

	s.addBubble(convID, bubble)

	// Save original cmdline before user may modify it
	originalCmdline := bubble.Cmdline

	// Wait for user answer
	ans := s.waitForAskAnswer(convID, askID, askType, payload)

	// Update bubble as answered
	s.mu.Lock()
	if conv, ok := s.convs[convID]; ok {
		for i := range conv.Messages {
			if conv.Messages[i].ID == askID {
				conv.Messages[i].Answered = true
				conv.Messages[i].Answer = ans.Answer
				conv.Messages[i].Approved = ans.Approved
				break
			}
		}
	}
	s.mu.Unlock()

	// Return answer to ACP subprocess
	w.Header().Set("Content-Type", "text/plain")
	switch askType {
	case "ask_exec":
		if ans.Approved {
			// Execute the command and return output to the ACP subprocess
			timeout := time.Duration(ans.Timeout) * time.Second
			if timeout == 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", ans.Cmdline)
			cmd.Dir = s.rootDir
			start := time.Now()
			out, err := cmd.CombinedOutput()
			duration := time.Since(start)

			var buf bytes.Buffer
			if ans.Cmdline != originalCmdline {
				buf.WriteString(fmt.Sprintf("User modified the command, running: %s\n", ans.Cmdline))
			}
			buf.Write(out)
			if exitErr, ok := err.(*exec.ExitError); ok {
				buf.WriteString(fmt.Sprintf("\nExit status: %d", exitErr.ExitCode()))
			} else if err != nil {
				buf.WriteString(fmt.Sprintf("\nExit status: 1"))
			} else {
				buf.WriteString(fmt.Sprintf("\nExit status: 0"))
			}
			buf.WriteString(fmt.Sprintf("\nDuration: %.3fs", duration.Seconds()))
			w.Write(buf.Bytes())
		} else {
			w.WriteHeader(200)
			w.Write([]byte("DENIED: " + ans.DenyReason))
		}
	default:
		w.Write([]byte(ans.Answer))
	}
}

func (s *Server) handleAskAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/ask/"), "/")
	askID := parts[0]

	var ans AskAnswer
	if err := json.NewDecoder(r.Body).Decode(&ans); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.askMu.Lock()
	ch, ok := s.askPends[askID]
	if ok {
		delete(s.askPends, askID)
	}
	s.askMu.Unlock()

	if ch != nil {
		ch <- &ans
	}

	w.WriteHeader(204)
}

func (s *Server) waitForAskAnswer(convID, askID, askType string, payload interface{}) *AskAnswer {
	ch := make(chan *AskAnswer, 1)
	s.askMu.Lock()
	s.askPends[askID] = ch
	s.askMu.Unlock()

	data, _ := json.Marshal(payload)
	s.broadcastSSE(convID, askType, json.RawMessage(data))

	// Extract a readable title from payload
	var title string
	switch askType {
	case "ask_open_ended", "ask_multiple_choice":
		if m, ok := payload.(map[string]json.RawMessage); ok {
			json.Unmarshal(m["question"], &title)
		}
	case "ask_exec":
		if m, ok := payload.(map[string]json.RawMessage); ok {
			json.Unmarshal(m["cmdline"], &title)
		}
	}
	s.pushNotif("ask", convID, title)

	return <-ch
}

// ── SSE ─────────────────────────────────────────────────────────────────────

func (s *Server) handleTasksRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	tools.TasksMu.Lock()
	defer tools.TasksMu.Unlock()
	tasks, err := tools.ReadTasks()
	if err != nil {
		// No tasks file = empty list
		writeJSON(w, []interface{}{})
		return
	}
	writeJSON(w, tasks)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.ID == "" {
		http.Error(w, "missing id", 400)
		return
	}

	tools.TasksMu.Lock()
	defer tools.TasksMu.Unlock()
	tasks, err := tools.ReadTasks()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	prefix := req.ID + "."
	var filtered []tools.Task
	found := false
	for _, t := range tasks {
		if t.ID == req.ID || strings.HasPrefix(t.ID, prefix) {
			found = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		writeJSON(w, map[string]interface{}{"ok": false, "tasks": tasks})
		return
	}
	if err := tools.WriteTasks(filtered); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "tasks": filtered})
}

func (s *Server) handleTasksClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	tools.TasksMu.Lock()
	defer tools.TasksMu.Unlock()
	os.Remove(tools.TasksFilePath())
	writeJSON(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan SSEEvent, 64)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcastSSE(convID, event string, data interface{}) {
	raw, _ := json.Marshal(data)
	s.dlog.Log("broadcastSSE conv=%s event=%s data_len=%d clients=%d", convID, event, len(raw), len(s.sseClients))
	ev := SSEEvent{
		ConversationID: convID,
		Event:          event,
		Data:          raw,
	}
	s.sseMu.RLock()
	defer s.sseMu.RUnlock()
	for ch := range s.sseClients {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ── JSONL persistence (one file per conversation) ──────────────────────────

func (s *Server) convDir() string {
	return filepath.Join(s.rootDir, ".llmdevkit", "conversations")
}

func (s *Server) convFile(convID string) string {
	return filepath.Join(s.convDir(), convID+".jsonl")
}

func (s *Server) appendJSONL(convID, eventType string, payload interface{}) {
	dir := s.convDir()
	os.MkdirAll(dir, 0755)

	f, err := os.OpenFile(s.convFile(convID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("jsonl open %s: %v", convID, err)
		return
	}
	defer f.Close()

	payloadRaw, _ := json.Marshal(payload)
	line := jsonlLine{Type: eventType, Payload: payloadRaw}
	data, _ := json.Marshal(line)
	f.Write(data)
	f.Write([]byte("\n"))
}

func (s *Server) loadConversations() error {
	dir := s.convDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	convs := make(map[string]*Conversation)
	var order []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		convID := strings.TrimSuffix(entry.Name(), ".jsonl")

		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		conv := &Conversation{
			ID:       convID,
			Messages: []BubbleMessage{},
		}

		var pendingTC int
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			var line jsonlLine
			if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
				continue
			}
			switch line.Type {
			case "conversation_created":
				var c Conversation
				json.Unmarshal(line.Payload, &c)
				if c.ID != "" {
					conv.ID = c.ID
				}
				if c.Agent != "" {
					conv.Agent = c.Agent
				}
				if c.SystemPrompt != "" {
					conv.SystemPrompt = c.SystemPrompt
				}
				if c.Title != "" {
					conv.Title = c.Title
				}

			case "init":
				var data struct {
					Agent        string   `json:"agent"`
					ACPSession   string   `json:"acp_session"`
					SystemPrompt string   `json:"system_prompt"`
					Tools        []string `json:"tools"`
				}
				json.Unmarshal(line.Payload, &data)
				conv.Agent = data.Agent
				conv.ACPSessionID = data.ACPSession
				conv.Initialized = true
				if data.SystemPrompt != "" {
					conv.SystemPrompt = data.SystemPrompt
				}
				if data.Tools != nil {
					conv.Tools = data.Tools
				}

			case "bubble":
				var b BubbleMessage
				json.Unmarshal(line.Payload, &b)
				if b.Type == "llm" && pendingTC > 0 {
					b.TokenCount = pendingTC
					pendingTC = 0
				}
				conv.Messages = append(conv.Messages, b)

			case "bubble_merge":
				var b BubbleMessage
				json.Unmarshal(line.Payload, &b)
				if len(conv.Messages) > 0 {
					last := &conv.Messages[len(conv.Messages)-1]
					if last.Type == b.Type {
						last.Content += b.Content
					}
				}

			case "prompt":
				var data struct {
					Prompt string `json:"prompt"`
				}
				json.Unmarshal(line.Payload, &data)
				if data.Prompt != "" {
					conv.Messages = append(conv.Messages, BubbleMessage{Type: "user", Content: data.Prompt})
				}

			case "prompt_response":
				// informational only

			case "token_stats":
				var ts TokenStats
				json.Unmarshal(line.Payload, &ts)
				pendingTC = ts.TotalTokens
			}
		}
		f.Close()

		// Populate file size
		if fi, err := os.Stat(filepath.Join(dir, entry.Name())); err == nil {
			conv.FileSize = fi.Size()
		}

		convs[convID] = conv
		order = append(order, convID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs = convs
	s.convOrder = order
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(500)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Suppress unused
var _ = io.ReadAll
