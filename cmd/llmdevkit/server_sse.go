package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"llmdevkit/internal/tools"
)

// -- Ask tool handling -------------------------------------------------------

// -- Side-channel for ask tools (called by llmdevkit acp subprocess) ----------

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
			conv.LastPromptTokens = stats.PromptTokens
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

// -- Tasks API ---------------------------------------------------------------

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

// -- SSE ---------------------------------------------------------------------

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
		Data:           raw,
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

// -- JSONL persistence (one file per conversation) --------------------------

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
				if c.LLM != "" {
					conv.LLM = c.LLM
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

			case "tool_request_rawinput":
				var data struct {
					ToolCallID string `json:"toolCallId"`
					RawInput   string `json:"rawInput"`
				}
				if err := json.Unmarshal(line.Payload, &data); err == nil {
					for i := len(conv.Messages) - 1; i >= 0; i-- {
						m := &conv.Messages[i]
						if m.Type == "tool_request" {
							var parsed map[string]json.RawMessage
							if err := json.Unmarshal([]byte(m.Content), &parsed); err == nil {
								if tcID, ok := parsed["toolCallId"]; ok {
									var idStr string
									if err := json.Unmarshal(tcID, &idStr); err == nil && idStr == data.ToolCallID {
										parsed["rawInput"] = json.RawMessage(data.RawInput)
										updated, _ := json.Marshal(parsed)
										m.Content = string(updated)
										break
									}
								}
							}
						}
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

			case "llm_change":
				var data struct {
					LLM string `json:"llm"`
				}
				json.Unmarshal(line.Payload, &data)
				if data.LLM != "" {
					conv.LLM = data.LLM
				}

			case "token_stats":
				var ts TokenStats
				json.Unmarshal(line.Payload, &ts)
				pendingTC = ts.TotalTokens
				conv.LastPromptTokens = ts.PromptTokens
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

// -- Helpers -----------------------------------------------------------------

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
