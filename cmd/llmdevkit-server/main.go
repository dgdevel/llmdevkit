package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/llms"

	acp "github.com/ironpark/go-acp"
)

//go:embed ui.html
var staticFS embed.FS

// ── Data types ──────────────────────────────────────────────────────────────

type BubbleMessage struct {
	Type           string   `json:"type"`
	Content        string   `json:"content"`
	Name           string   `json:"name,omitempty"`
	ID             string   `json:"id,omitempty"`
	Cmdline        string   `json:"cmdline,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	Choices        []string `json:"choices,omitempty"`
	AllowOpenEnded bool     `json:"allow_open_ended,omitempty"`
	Question       string   `json:"question,omitempty"`
	Answered       bool     `json:"answered,omitempty"`
	Approved       bool     `json:"approved,omitempty"`
	Answer         string   `json:"answer,omitempty"`
}

type Conversation struct {
	ID           string          `json:"id"`
	Agent        string          `json:"agent"`
	SystemPrompt string          `json:"system_prompt,omitempty"`
	Tools        []string        `json:"tools,omitempty"`
	Title        string          `json:"title,omitempty"`
	Messages     []BubbleMessage `json:"messages"`

	ACPSessionID string `json:"-"`
	Initialized  bool   `json:"-"`
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
	mu       sync.RWMutex
	convs    map[string]*Conversation
	convOrder []string

	acpConn      *acp.ClientSideConnection
	acpCmd       *exec.Cmd
	acpMu        sync.Mutex
	acpConnected bool

	askMu    sync.Mutex
	askPends map[string]chan *AskAnswer

	sseMu      sync.RWMutex
	sseClients map[chan SSEEvent]struct{}
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

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	rootDir, _ := os.Getwd()
	rootDir, _ = filepath.Abs(rootDir)

	llmCfg, err := llms.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load llms config: %v", err)
	}

	agentCfg, err := agents.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load agents config: %v", err)
	}

	srv := &Server{
		rootDir:    rootDir,
		llmCfg:     llmCfg,
		agentCfg:   agentCfg,
		convs:      make(map[string]*Conversation),
		askPends:   make(map[string]chan *AskAnswer),
		sseClients: make(map[chan SSEEvent]struct{}),
	}

	if err := srv.loadConversations(); err != nil {
		log.Printf("warning: load conversations: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveUI)
	mux.HandleFunc("/api/agents", srv.handleAgents)
	mux.HandleFunc("/api/conversations", srv.handleConversations)
	mux.HandleFunc("/api/conversations/", srv.handleConversationActions)
	mux.HandleFunc("/api/ask/", srv.handleAskAnswer)
	mux.HandleFunc("/api/sidechannel", srv.handleSideChannel)
	mux.HandleFunc("/api/events", srv.handleSSE)

	addr := ":18681"
	log.Printf("llmdevkit-server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// ── Static UI ───────────────────────────────────────────────────────────────

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, _ := staticFS.ReadFile("ui.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
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

// ── API: Conversations ──────────────────────────────────────────────────────

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		var list []*Conversation
		for _, id := range s.convOrder {
			if c, ok := s.convs[id]; ok {
				list = append(list, c)
			}
		}
		s.mu.RUnlock()
		writeJSON(w, list)

	case http.MethodPost:
		var req struct {
			Agent        string `json:"agent"`
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		conv := &Conversation{
			ID:           fmt.Sprintf("conv_%d", time.Now().UnixNano()),
			Agent:        req.Agent,
			SystemPrompt: req.SystemPrompt,
			Messages:     []BubbleMessage{},
		}
		s.mu.Lock()
		s.convs[conv.ID] = conv
		s.convOrder = append([]string{conv.ID}, s.convOrder...)
		s.mu.Unlock()
		s.appendJSONL(conv.ID, "conversation_created", conv)
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
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.mu.Lock()
	conv, ok := s.convs[convID]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "conversation not found", 404)
		return
	}

	if err := s.ensureACPConnection(); err != nil {
		writeJSONError(w, fmt.Sprintf("ACP init: %v", err))
		return
	}

	s.acpMu.Lock()
	sessResp, err := s.acpConn.NewSession(r.Context(), &acp.NewSessionRequest{
		Cwd: s.rootDir,
	})
	s.acpMu.Unlock()
	if err != nil {
		writeJSONError(w, fmt.Sprintf("new session: %v", err))
		return
	}

	conv.ACPSessionID = string(sessResp.SessionID)
	conv.Initialized = true

	agentCfg, _ := s.agentCfg.Lookup(conv.Agent)
	if agentCfg != nil {
		sysPrompt := conv.SystemPrompt
		if sysPrompt == "" {
			sysPrompt = agentCfg.SystemPrompt
		}
		conv.SystemPrompt = sysPrompt
		conv.Tools = agentCfg.ToolNames()
	}

	conv.Messages = append(conv.Messages, BubbleMessage{
		Type:    "user",
		Content: req.Prompt,
	})

	s.appendJSONL(convID, "init", map[string]interface{}{
		"agent":       conv.Agent,
		"acp_session": conv.ACPSessionID,
	})
	s.broadcastSSE(convID, "state", map[string]bool{"running": true})

	go s.runACPPrompt(convID, req.Prompt)

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
		Prompt string `json:"prompt"`
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

	s.appendJSONL(convID, "prompt", map[string]string{"prompt": req.Prompt})
	s.broadcastSSE(convID, "state", map[string]bool{"running": true})

	go s.runACPPrompt(convID, req.Prompt)

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
	if conv.ACPSessionID != "" {
		s.acpMu.Lock()
		s.acpConn.Cancel(r.Context(), &acp.CancelNotification{
			SessionID: acp.SessionID(conv.ACPSessionID),
		})
		s.acpMu.Unlock()
	}
	s.broadcastSSE(convID, "state", map[string]bool{"running": false})
	w.WriteHeader(204)
}

// ── ACP subprocess management ───────────────────────────────────────────────

func (s *Server) ensureACPConnection() error {
	s.acpMu.Lock()
	defer s.acpMu.Unlock()

	if s.acpConnected {
		return nil
	}

	binPath, err := exec.LookPath("llmdevkit-acp")
	if err != nil {
		return fmt.Errorf("llmdevkit-acp not found in PATH: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = s.rootDir
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "LLMDEVKIT_SIDE_CHANNEL=http://localhost:18681/api/sidechannel")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llmdevkit-acp: %w", err)
	}

	s.acpCmd = cmd


	clientImpl := &acpClientHandler{server: s}
	conn := acp.NewClientSideConnection(clientImpl, stdin, stdout)

	ctx := context.Background()
	if err := conn.Start(ctx); err != nil {
		return fmt.Errorf("start ACP connection: %w", err)
	}

	s.acpConn = conn

	_, err = conn.Initialize(ctx, &acp.InitializeRequest{
		ClientCapabilities: &acp.ClientCapabilities{},
		ClientInfo: &acp.Implementation{
			Name:    "llmdevkit-server",
			Version: "0.1.0",
		},
		ProtocolVersion: 1,
	})
	if err != nil {
		return fmt.Errorf("ACP initialize: %w", err)
	}

	s.acpConnected = true
	return nil
}

// ── ACP Client implementation ───────────────────────────────────────────────

type acpClientHandler struct {
	server *Server
}

func (c *acpClientHandler) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	sid := string(params.SessionID)
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
		return nil
	}

	c.handleSessionUpdate(convID, params.Update)
	return nil
}

func (c *acpClientHandler) handleSessionUpdate(convID string, u acp.SessionUpdate) {
	raw, _ := json.Marshal(u)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if chunk, ok := m["agentMessageChunk"]; ok {
		var data struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		json.Unmarshal(chunk, &data)
		if data.Content.Text != "" {
			c.server.addBubble(convID, BubbleMessage{Type: "llm", Content: data.Content.Text})
		}
	}
	if chunk, ok := m["agentThoughtChunk"]; ok {
		var data struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		json.Unmarshal(chunk, &data)
		if data.Content.Text != "" {
			c.server.addBubble(convID, BubbleMessage{Type: "thinking", Content: data.Content.Text})
		}
	}
	if tc, ok := m["toolCall"]; ok {
		var data struct {
			Title string `json:"title"`
		}
		json.Unmarshal(tc, &data)
		c.server.addBubble(convID, BubbleMessage{Type: "tool_request", Name: data.Title, Content: string(tc)})
	}
	if tcu, ok := m["toolCallUpdate"]; ok {
		var data struct {
			ToolCallID string `json:"toolCallId"`
			Status     string `json:"status"`
			Content    []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		json.Unmarshal(tcu, &data)
		if data.Status == "completed" || data.Status == "failed" {
			var texts []string
			for _, ct := range data.Content {
				texts = append(texts, ct.Text)
			}
			content := strings.Join(texts, "\n")
			if content == "" {
				content = data.Status
			}
			c.server.addBubble(convID, BubbleMessage{Type: "tool_response", Name: data.ToolCallID, Content: content})
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
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	contentBlock := acp.NewContentBlockText(promptText)
	promptReq := &acp.PromptRequest{
		SessionID: acp.SessionID(conv.ACPSessionID),
		Prompt:    []acp.ContentBlock{contentBlock},
	}

	s.acpMu.Lock()
	resp, err := s.acpConn.Prompt(ctx, promptReq)
	s.acpMu.Unlock()

	if err != nil {
		s.addBubble(convID, BubbleMessage{Type: "error", Content: fmt.Sprintf("ACP prompt error: %v", err)})
	}

	if resp != nil {
		s.appendJSONL(convID, "prompt_response", map[string]string{
			"stop_reason": string(resp.StopReason),
		})
	}

	s.broadcastSSE(convID, "state", map[string]bool{"running": false})
}

// ── Bubble management ───────────────────────────────────────────────────────

func (s *Server) addBubble(convID string, b BubbleMessage) {
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

	conv.Messages = append(conv.Messages, b)
	s.mu.Unlock()

	s.broadcastSSE(convID, "session_update", b)
	s.appendJSONL(convID, "bubble", b)
}

// ── Ask tool handling ───────────────────────────────────────────────────────

// ── Side-channel for ask tools (called by llmdevkit-acp subprocess) ──────────

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

	// Find the active conversation (last one with running state)
	s.mu.RLock()
	var convID string
	for _, id := range s.convOrder {
		if c, ok := s.convs[id]; ok && c.Initialized {
			convID = c.ID
			break
		}
	}
	s.mu.RUnlock()

	if convID == "" {
		http.Error(w, "no active conversation", 400)
		return
	}

	askID := fmt.Sprintf("ask_%d", time.Now().UnixNano())

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
			w.Write([]byte(ans.Cmdline))
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

	return <-ch
}

// ── SSE ─────────────────────────────────────────────────────────────────────

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

			case "init":
				var data struct {
					Agent      string `json:"agent"`
					ACPSession string `json:"acp_session"`
				}
				json.Unmarshal(line.Payload, &data)
				conv.Agent = data.Agent
				conv.ACPSessionID = data.ACPSession
				conv.Initialized = true

			case "bubble":
				var b BubbleMessage
				json.Unmarshal(line.Payload, &b)
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
				// already handled via bubbles

			case "prompt_response":
				// informational only
			}
		}
		f.Close()

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
