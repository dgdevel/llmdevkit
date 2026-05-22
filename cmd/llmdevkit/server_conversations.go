package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llmdevkit/internal/agents"

	acp "github.com/ironpark/go-acp"
)

// -- API: Conversations ------------------------------------------------------

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
		// Set default LLM from agent config
		if s.agentCfg != nil {
			if ac, ok := s.agentCfg.Lookup(req.Agent); ok && ac.LLM != "" {
				conv.LLM = ac.LLM
			}
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
	case "llm_change":
		s.handleConvLLMChange(w, r, convID)
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
				sysPrompt += "\n\n# AGENTS.md - Project Specific Instructions\n\n" + string(agentsMD)
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
		"agent":         conv.Agent,
		"acp_session":   conv.ACPSessionID,
		"system_prompt": conv.SystemPrompt,
		"tools":         conv.Tools,
	})
	s.appendJSONL(convID, "bubble", BubbleMessage{Type: "user", Content: req.Prompt, Timestamp: nowISO()})
	s.setConvRunning(convID, true)
	// Snapshot conv for SSE broadcast -- goroutine may modify conv concurrently
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
	// Cancel the server-side prompt context so runACPPrompt returns promptly
	s.mu.Lock()
	if conv.PromptCancel != nil {
		conv.PromptCancel()
		conv.PromptCancel = nil
	}
	s.mu.Unlock()
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

func (s *Server) handleConvLLMChange(w http.ResponseWriter, r *http.Request, convID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		LLM string `json:"llm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	conv, ok := s.convs[convID]
	if ok && req.LLM != "" {
		conv.LLM = req.LLM
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	s.appendJSONL(convID, "llm_change", map[string]string{"llm": conv.LLM})
	s.broadcastSSE("", "conversation_updated", conv)
	writeJSON(w, map[string]string{"llm": conv.LLM})
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
