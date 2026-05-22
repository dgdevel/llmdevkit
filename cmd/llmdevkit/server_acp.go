package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"time"
	"unsafe"

	acp "github.com/ironpark/go-acp"
)

// -- ACP subprocess management -----------------------------------------------

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

// -- ACP Client implementation -----------------------------------------------

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
		// Handle rawInput update -- update the tool_request bubble with arguments
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

// -- ACP prompt execution ----------------------------------------------------

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
		// Fresh ACP process -- need a new session
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

	// Store cancel so handleConvCancel can abort this context
	s.mu.Lock()
	conv.PromptCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		conv.PromptCancel = nil
		s.mu.Unlock()
	}()

	// Mark continuation if we reconnected and conversation has prior messages
	isContinuation := justConnected && len(conv.Messages) > 1

	// On continuation, reconstruct chat history from bubbles so the fresh
	// agent process receives the full conversation context.
	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var historyBlocks []chatMsg
	if isContinuation {
		for _, b := range conv.Messages {
			switch b.Type {
			case "user":
				historyBlocks = append(historyBlocks, chatMsg{Role: "user", Content: b.Content})
			case "llm":
				historyBlocks = append(historyBlocks, chatMsg{Role: "assistant", Content: b.Content})
			case "tool_response":
				historyBlocks = append(historyBlocks, chatMsg{Role: "tool", Content: b.Content})
			}
		}
	}

	contentBlock := acp.NewContentBlockText(promptText)
	promptReq := &acp.PromptRequest{
		SessionID: acp.SessionID(conv.ACPSessionID),
		Prompt:    []acp.ContentBlock{contentBlock},
	}
	meta := map[string]any{}
	if conv.LLM != "" {
		meta["llm"] = conv.LLM
	}
	if isContinuation {
		meta["continuation"] = true
		if len(historyBlocks) > 0 {
			historyJSON, _ := json.Marshal(historyBlocks)
			meta["history"] = json.RawMessage(historyJSON)
		}
	}
	if len(meta) > 0 {
		promptReq.Meta = meta
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

// -- Bubble management -------------------------------------------------------

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
