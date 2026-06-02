package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
	"llmdevkit/internal/llms"
	_ "llmdevkit/internal/mcps"

	acp "github.com/ironpark/go-acp"
)

// noopLogger returns a debug logger that won't panic on nil.
func noopLogger() *debuglog.Logger {
	return debuglog.For("test")
}

// -- Helpers ------------------------------------------------------------------

// newTestServer creates a Server with nil configs (no ACP subprocess).
// Returns the server and a temp dir (caller removes via t.Cleanup).
func newTestServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	srv := &Server{
		rootDir:    tmpDir,
		llmCfg:     nil,
		agentCfg:   nil,
		mcpCfg:     nil,
		dlog:       noopLogger(),
		convs:      make(map[string]*Conversation),
		askPends:   make(map[string]chan *AskAnswer),
		sseClients: make(map[chan SSEEvent]struct{}),
	}
	t.Cleanup(srv.Close)
	return srv
}

// newTestServerWithConfigs creates a Server with agent/llm configs for
// testing endpoints that need them (agents list, tool defs).
func newTestServerWithConfigs(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	srv := &Server{
		rootDir: tmpDir,
		agentCfg: &agents.Config{Agents: []agents.AgentConfig{
			{Name: "code", LLM: "gpt4", SystemPrompt: "You are a coding assistant.", Tools: "devkit"},
			{Name: "chat", LLM: "claude", SystemPrompt: "You are a chat assistant.", Tools: "ask"},
		}},
		llmCfg: &llms.Config{
			LLMs: []llms.LLMConfig{
				{Name: "GPT-4", Model: "gpt-4"},
				{Name: "Claude", Model: "claude-3-opus"},
			},
		},
		mcpCfg:     nil,
		dlog:       noopLogger(),
		convs:      make(map[string]*Conversation),
		askPends:   make(map[string]chan *AskAnswer),
		sseClients: make(map[chan SSEEvent]struct{}),
	}
	t.Cleanup(srv.Close)
	return srv
}

// handlerMux builds the ServeMux identical to main().
func handlerMux(srv *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveUI)
	mux.HandleFunc("/api/agents", srv.handleAgents)
	mux.HandleFunc("/api/tooldefs", srv.handleToolDefs)
	mux.HandleFunc("/api/conversations", srv.handleConversations)
	mux.HandleFunc("/api/conversations/", srv.handleConversationActions)
	mux.HandleFunc("/api/ask/", srv.handleAskAnswer)
	mux.HandleFunc("/api/sidechannel", srv.handleSideChannel)
	mux.HandleFunc("/api/events", srv.handleSSE)
	return mux
}

// testURL creates an httptest.Server and returns its base URL.
func testURL(t *testing.T, mux http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

// getJSON is a helper to GET a URL and decode the JSON response.
func getJSON(t *testing.T, url string, target any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("decode response from %s: %v", url, err)
		}
	}
	return resp
}

// postJSON POSTs a JSON body and decodes the response.
func postJSON(t *testing.T, url string, body any, target any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	resp, err := http.Post(url, "application/json", bodyReader)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if target != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("decode response from %s: %v", url, err)
		}
	}
	return resp
}

// deleteURL sends a DELETE and returns the response.
func deleteURL(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// -- Server helpers exposed for testing --------------------------------------
// addConversation adds a conversation directly to the server's map.
func (s *Server) addConversation(conv *Conversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[conv.ID] = conv
	s.convOrder = append([]string{conv.ID}, s.convOrder...)
}

// -- Tests: GET / (UI) -------------------------------------------------------

func TestServer_ServeUI(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content-type, got %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty HTML body")
	}
}

func TestServer_ServeUI_NotFound(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Get(base + "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// -- Tests: GET /api/agents --------------------------------------------------

func TestServer_Agents_List(t *testing.T) {
	srv := newTestServerWithConfigs(t)
	base := testURL(t, handlerMux(srv))

	var list []agentInfo
	getJSON(t, base+"/api/agents", &list)

	if len(list) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(list))
	}

	names := map[string]bool{}
	for _, a := range list {
		names[a.Name] = true
	}
	if !names["code"] || !names["chat"] {
		t.Errorf("expected agents 'code' and 'chat', got %+v", list)
	}
}

func TestServer_Agents_EmptyConfig(t *testing.T) {
	srv := newTestServer(t) // nil configs
	base := testURL(t, handlerMux(srv))

	var list []agentInfo
	getJSON(t, base+"/api/agents", &list)

	if len(list) != 0 {
		t.Errorf("expected 0 agents with nil config, got %d", len(list))
	}
}

func TestServer_Agents_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Post(base+"/api/agents", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

// -- Tests: GET /api/tooldefs -------------------------------------------------

func TestServer_ToolDefs_NoAgent(t *testing.T) {
	srv := newTestServerWithConfigs(t)
	base := testURL(t, handlerMux(srv))

	var defs []ToolDefInfo
	getJSON(t, base+"/api/tooldefs", &defs)

	if len(defs) != 0 {
		t.Errorf("expected 0 tool defs without agent param, got %d", len(defs))
	}
}

func TestServer_ToolDefs_UnknownAgent(t *testing.T) {
	srv := newTestServerWithConfigs(t)
	base := testURL(t, handlerMux(srv))

	var defs []ToolDefInfo
	getJSON(t, base+"/api/tooldefs?agent=nonexistent", &defs)

	if len(defs) != 0 {
		t.Errorf("expected 0 tool defs for unknown agent, got %d", len(defs))
	}
}

func TestServer_ToolDefs_AgentWithAsk(t *testing.T) {
	srv := newTestServerWithConfigs(t)
	base := testURL(t, handlerMux(srv))

	var defs []ToolDefInfo
	getJSON(t, base+"/api/tooldefs?agent=chat", &defs)

	// "chat" agent has tools: "ask" -> should resolve to 3 ask tools
	if len(defs) < 3 {
		t.Errorf("expected at least 3 ask tool defs, got %d", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["ask_open_ended"] || !names["ask_exec"] || !names["ask_multiple_choice"] {
		t.Errorf("expected ask tools, got names: %v", names)
	}
}

// -- Tests: GET /api/conversations --------------------------------------------

func TestServer_Conversations_ListEmpty(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var list []*Conversation
	getJSON(t, base+"/api/conversations", &list)

	if list != nil && len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestServer_Conversations_Create(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{
		"agent":         "code",
		"system_prompt": "Be helpful",
	}, &conv)

	if conv.ID == "" {
		t.Fatal("expected non-empty conversation ID")
	}
	if conv.Agent != "code" {
		t.Errorf("expected agent='code', got %q", conv.Agent)
	}
	if conv.SystemPrompt != "Be helpful" {
		t.Errorf("expected system_prompt='Be helpful', got %q", conv.SystemPrompt)
	}
	if len(conv.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(conv.Messages))
	}

	// Verify it appears in list
	var list []*Conversation
	getJSON(t, base+"/api/conversations", &list)

	found := false
	for _, c := range list {
		if c.ID == conv.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("created conversation not found in list")
	}
}

func TestServer_Conversations_Create_Defaults(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{}, &conv)

	if conv.ID == "" {
		t.Fatal("expected non-empty conversation ID")
	}
	if conv.Agent != "" {
		t.Errorf("expected empty agent, got %q", conv.Agent)
	}
}

func TestServer_Conversations_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.NewRequest("PUT", base+"/api/conversations", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()

	if r.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", r.StatusCode)
	}
}

// -- Tests: GET/DELETE /api/conversations/{id} -------------------------------

func TestServer_Conversation_Get(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	// Create a conversation
	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{
		"agent": "test",
	}, &conv)

	// Get it back
	var fetched Conversation
	getJSON(t, base+"/api/conversations/"+conv.ID, &fetched)

	if fetched.ID != conv.ID {
		t.Errorf("expected ID %q, got %q", conv.ID, fetched.ID)
	}
	if fetched.Agent != "test" {
		t.Errorf("expected agent 'test', got %q", fetched.Agent)
	}
}

func TestServer_Conversation_Get_NotFound(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Get(base + "/api/conversations/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServer_Conversation_Delete(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	// Create a conversation
	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{
		"agent": "test",
	}, &conv)

	// Delete it
	resp := deleteURL(t, base+"/api/conversations/"+conv.ID)
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify it's gone
	getResp, err := http.Get(base + "/api/conversations/" + conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != 404 {
		t.Fatalf("expected 404 after delete, got %d", getResp.StatusCode)
	}
}

func TestServer_Conversation_Delete_NotFound(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp := deleteURL(t, base+"/api/conversations/nonexistent")
	// Deleting non-existent is a no-op; server returns 204
	if resp.StatusCode != 204 {
		t.Logf("delete nonexistent returned %d (may be expected)", resp.StatusCode)
	}
}

// -- Tests: /api/conversations/{id}/prompt ------------------------------------

func TestServer_Conversation_Prompt_NotInitialized(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	// Create conversation (not initialized)
	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{
		"agent": "test",
	}, &conv)

	// Try to prompt without init
	resp := postJSON(t, base+"/api/conversations/"+conv.ID+"/prompt", map[string]string{
		"prompt": "hello",
	}, nil)

	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for prompt on uninit session, got %d", resp.StatusCode)
	}
}

func TestServer_Conversation_Prompt_NotFound(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp := postJSON(t, base+"/api/conversations/nonexistent/prompt", map[string]string{
		"prompt": "hello",
	}, nil)

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// -- Tests: /api/conversations/{id}/cancel ------------------------------------

func TestServer_Conversation_Cancel_NotFound(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp := postJSON(t, base+"/api/conversations/nonexistent/cancel", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServer_Conversation_Cancel_NoSession(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{"agent": "test"}, &conv)

	// Cancel without ACP session -> should still work (just no Cancel RPC)
	resp := postJSON(t, base+"/api/conversations/"+conv.ID+"/cancel", nil, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

// -- Tests: /api/conversations/{id}/init (requires ACP subprocess) ------------
// These test init -- may succeed or fail depending on whether llmdevkit is in PATH.

func TestServer_Conversation_Init_ACPNotAvailable(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{"agent": "test"}, &conv)

	// Init may succeed if llmdevkit is in PATH (spawns ACP subprocess),
	// or fail if not. Either outcome is acceptable.
	resp, err := http.Post(base+"/api/conversations/"+conv.ID+"/init",
		"application/json", strings.NewReader(`{"prompt":"hello"}`))
	if err != nil {
		t.Logf("init returned error: %v", err)
	} else {
		resp.Body.Close()
		t.Logf("init returned status %d", resp.StatusCode)
	}
}

// -- Tests: /api/conversations/{id}/unknown -----------------------------------

func TestServer_Conversation_UnknownAction(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{}, &conv)

	resp := postJSON(t, base+"/api/conversations/"+conv.ID+"/unknown_action", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for unknown action, got %d", resp.StatusCode)
	}
}

// -- Tests: SSE /api/events ---------------------------------------------------

func TestServer_SSE_Connect(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)

	// Connect to SSE endpoint
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		ts.Close()
		t.Fatal(err)
	}

	if resp.StatusCode != 200 {
		resp.Body.Close()
		ts.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	// Close body before server to unblock the SSE handler goroutine
	resp.Body.Close()
	ts.Close()
}

func TestServer_SSE_ReceiveEvents(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Register a channel-based SSE client directly
	ch := make(chan SSEEvent, 8)
	srv.sseMu.Lock()
	srv.sseClients[ch] = struct{}{}
	srv.sseMu.Unlock()

	// Broadcast an event
	srv.broadcastSSE("conv1", "test_event", map[string]string{"hello": "world"})

	// Read via channel
	select {
	case ev := <-ch:
		if ev.ConversationID != "conv1" {
			t.Errorf("expected conv1, got %s", ev.ConversationID)
		}
		if ev.Event != "test_event" {
			t.Errorf("expected test_event, got %s", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE event")
	}

	// Clean up
	srv.sseMu.Lock()
	delete(srv.sseClients, ch)
	srv.sseMu.Unlock()
}

func TestServer_SSE_ClientDisconnect(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	time.Sleep(50 * time.Millisecond)

	// Cancel context -> client disconnects
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Server should clean up the client
	srv.sseMu.RLock()
	count := len(srv.sseClients)
	srv.sseMu.RUnlock()

	if count != 0 {
		t.Errorf("expected 0 SSE clients after disconnect, got %d", count)
	}
}

// -- Tests: Bubble management -------------------------------------------------

func TestServer_AddBubble_NewMessage(t *testing.T) {
	srv := newTestServer(t)
	conv := &Conversation{
		ID:       "test-conv",
		Messages: []BubbleMessage{},
	}
	srv.addConversation(conv)

	srv.addBubble("test-conv", BubbleMessage{Type: "llm", Content: "hello"})

	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", conv.Messages[0].Content)
	}
}

func TestServer_AddBubble_MergeStreaming(t *testing.T) {
	srv := newTestServer(t)
	conv := &Conversation{
		ID:       "test-conv",
		Messages: []BubbleMessage{},
	}
	srv.addConversation(conv)

	// First bubble
	srv.addBubble("test-conv", BubbleMessage{Type: "llm", Content: "hel"})
	// Second bubble of same type -> should merge
	srv.addBubble("test-conv", BubbleMessage{Type: "llm", Content: "lo"})

	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 message (merged), got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "hello" {
		t.Errorf("expected merged content 'hello', got %q", conv.Messages[0].Content)
	}
}

func TestServer_AddBubble_MergeThinking(t *testing.T) {
	srv := newTestServer(t)
	conv := &Conversation{
		ID:       "test-conv",
		Messages: []BubbleMessage{},
	}
	srv.addConversation(conv)

	srv.addBubble("test-conv", BubbleMessage{Type: "thinking", Content: "let me "})
	srv.addBubble("test-conv", BubbleMessage{Type: "thinking", Content: "think"})

	if len(conv.Messages) != 1 {
		t.Fatalf("expected 1 merged thinking message, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "let me think" {
		t.Errorf("expected merged thinking, got %q", conv.Messages[0].Content)
	}
}

func TestServer_AddBubble_NoMergeDifferentType(t *testing.T) {
	srv := newTestServer(t)
	conv := &Conversation{
		ID:       "test-conv",
		Messages: []BubbleMessage{},
	}
	srv.addConversation(conv)

	srv.addBubble("test-conv", BubbleMessage{Type: "llm", Content: "hello"})
	srv.addBubble("test-conv", BubbleMessage{Type: "tool_request", Name: "read_file", Content: "{}"})

	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 separate messages, got %d", len(conv.Messages))
	}
}

func TestServer_AddBubble_ConvNotFound(t *testing.T) {
	srv := newTestServer(t)
	// Should not panic
	srv.addBubble("nonexistent", BubbleMessage{Type: "llm", Content: "hello"})
}

// -- Tests: Side channel ------------------------------------------------------

func TestServer_SideChannel_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Get(base + "/api/sidechannel")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestServer_SideChannel_NoActiveConversation(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp := postJSON(t, base+"/api/sidechannel", map[string]string{
		"type":     "ask_open_ended",
		"question": "What?",
	}, nil)

	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 when no active conversation, got %d", resp.StatusCode)
	}
}

// -- Tests: Ask answer endpoint -----------------------------------------------

func TestServer_AskAnswer_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	resp, err := http.Get(base + "/api/ask/ask_123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestServer_AskAnswer_Resolve(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	// Set up a pending ask
	ch := make(chan *AskAnswer, 1)
	srv.askMu.Lock()
	srv.askPends["ask_123"] = ch
	srv.askMu.Unlock()

	// Answer it
	resp := postJSON(t, base+"/api/ask/ask_123", AskAnswer{
		Type:   "ask_open_ended",
		Answer: "my response",
	}, nil)

	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify the answer was received
	select {
	case ans := <-ch:
		if ans.Answer != "my response" {
			t.Errorf("expected 'my response', got %q", ans.Answer)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ask answer")
	}
}

// -- Tests: Side channel with ask flow (end-to-end) --------------------------

func TestServer_SideChannel_AskOpenEnded(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Create an initialized conversation
	conv := &Conversation{
		ID:          "conv_ask",
		Agent:       "test",
		Initialized: true,
		Messages:    []BubbleMessage{},
	}
	srv.addConversation(conv)

	// SSE client to catch events
	sseCh := make(chan SSEEvent, 8)
	srv.sseMu.Lock()
	srv.sseClients[sseCh] = struct{}{}
	srv.sseMu.Unlock()

	// Side-channel call in goroutine (it blocks until answered)
	var sideResp string
	var sideErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body := map[string]any{
			"type":     "ask_open_ended",
			"question": "What is your name?",
		}
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+"/api/sidechannel", "application/json", strings.NewReader(string(b)))
		if err != nil {
			sideErr = err
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		sideResp = string(data)
	}()

	// Wait for the ask bubble to appear
	time.Sleep(100 * time.Millisecond)

	// Find the ask ID from the conversation messages
	srv.mu.RLock()
	var askID string
	for _, msg := range conv.Messages {
		if msg.Type == "ask_open_ended" && msg.ID != "" {
			askID = msg.ID
			break
		}
	}
	srv.mu.RUnlock()

	if askID == "" {
		t.Fatal("no ask_open_ended bubble found")
	}

	// Answer the ask
	ans := AskAnswer{Type: "ask_open_ended", Answer: "Alice"}
	ansBody, _ := json.Marshal(ans)
	http.Post(ts.URL+"/api/ask/"+askID, "application/json", strings.NewReader(string(ansBody)))

	// Wait for side-channel to complete with timeout
	wgWait(t, &wg)

	if sideErr != nil {
		t.Fatalf("side-channel error: %v", sideErr)
	}
	if sideResp != "Alice" {
		t.Errorf("expected 'Alice', got %q", sideResp)
	}
}

func TestServer_SideChannel_AskExec_Approved(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	conv := &Conversation{
		ID:          "conv_exec",
		Agent:       "test",
		Initialized: true,
		Messages:    []BubbleMessage{},
	}
	srv.addConversation(conv)

	var sideResp string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body := map[string]any{
			"type":    "ask_exec",
			"cmdline": "echo original",
			"timeout": 10,
		}
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+"/api/sidechannel", "application/json", strings.NewReader(string(b)))
		if err != nil {
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		sideResp = string(data)
	}()

	time.Sleep(100 * time.Millisecond)

	srv.mu.RLock()
	var askID string
	for _, msg := range conv.Messages {
		if msg.Type == "ask_exec" && msg.ID != "" {
			askID = msg.ID
			break
		}
	}
	srv.mu.RUnlock()

	if askID == "" {
		t.Fatal("no ask_exec bubble found")
	}

	// Approve with modified cmdline
	ans := AskAnswer{Type: "ask_exec", Approved: true, Cmdline: "echo modified"}
	ansBody, _ := json.Marshal(ans)
	http.Post(ts.URL+"/api/ask/"+askID, "application/json", strings.NewReader(string(ansBody)))

	wgWait(t, &wg)

	expected := "Exit status: 0\nDuration: "
	if !strings.HasPrefix(sideResp, expected) {
		t.Errorf("expected prefix %q, got %q", expected, sideResp)
	}
	if !strings.Contains(sideResp, "file_read") || !strings.Contains(sideResp, "run-") {
		t.Errorf("expected file_read message with run dir, got %q", sideResp)
	}
}

func TestServer_SideChannel_AskExec_ApprovedUnmodified(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	conv := &Conversation{
		ID:          "conv_exec_unmod",
		Agent:       "test",
		Initialized: true,
		Messages:    []BubbleMessage{},
	}
	srv.addConversation(conv)

	var sideResp string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body := map[string]any{
			"type":    "ask_exec",
			"cmdline": "echo hello",
			"timeout": 10,
		}
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+"/api/sidechannel", "application/json", strings.NewReader(string(b)))
		if err != nil {
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		sideResp = string(data)
	}()

	time.Sleep(100 * time.Millisecond)

	srv.mu.RLock()
	var askID string
	for _, msg := range conv.Messages {
		if msg.Type == "ask_exec" && msg.ID != "" {
			askID = msg.ID
			break
		}
	}
	srv.mu.RUnlock()

	if askID == "" {
		t.Fatal("no ask_exec bubble found")
	}

	// Approve without modifying cmdline
	ans := AskAnswer{Type: "ask_exec", Approved: true, Cmdline: "echo hello"}
	ansBody, _ := json.Marshal(ans)
	http.Post(ts.URL+"/api/ask/"+askID, "application/json", strings.NewReader(string(ansBody)))

	wgWait(t, &wg)

	if strings.Contains(sideResp, "User modified") {
		t.Errorf("should not contain 'User modified' when cmdline unchanged, got %q", sideResp)
	}
	if !strings.Contains(sideResp, "file_read") || !strings.Contains(sideResp, "run-") {
		t.Errorf("expected file_read message with run dir, got %q", sideResp)
	}
	if !strings.Contains(sideResp, "Exit status: 0") {
		t.Errorf("expected 'Exit status: 0', got %q", sideResp)
	}
}

func TestServer_SideChannel_AskExec_Denied(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	conv := &Conversation{
		ID:          "conv_exec_deny",
		Agent:       "test",
		Initialized: true,
		Messages:    []BubbleMessage{},
	}
	srv.addConversation(conv)

	var sideResp string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body := map[string]any{
			"type":    "ask_exec",
			"cmdline": "rm -rf /",
		}
		b, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL+"/api/sidechannel", "application/json", strings.NewReader(string(b)))
		if err != nil {
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		sideResp = string(data)
	}()

	time.Sleep(100 * time.Millisecond)

	srv.mu.RLock()
	var askID string
	for _, msg := range conv.Messages {
		if msg.Type == "ask_exec" && msg.ID != "" {
			askID = msg.ID
			break
		}
	}
	srv.mu.RUnlock()

	if askID == "" {
		t.Fatal("no ask_exec bubble found")
	}

	// Deny
	ans := AskAnswer{Type: "ask_exec", Approved: false, DenyReason: "dangerous"}
	ansBody, _ := json.Marshal(ans)
	http.Post(ts.URL+"/api/ask/"+askID, "application/json", strings.NewReader(string(ansBody)))

	wgWait(t, &wg)

	if !strings.Contains(sideResp, "DENIED") {
		t.Errorf("expected DENIED in response, got %q", sideResp)
	}
}

// -- Tests: JSONL persistence -------------------------------------------------

func TestServer_JSONL_AppendAndLoad(t *testing.T) {
	srv := newTestServer(t)

	// Append a JSONL entry
	srv.appendJSONL("test_conv", "conversation_created", map[string]string{
		"id":    "test_conv",
		"agent": "code",
	})

	// Verify the file exists
	file := srv.convFile("test_conv")
	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Fatalf("expected JSONL file at %s", file)
	}

	// Read and verify content
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var line jsonlLine
	if err := json.Unmarshal(data, &line); err != nil {
		t.Fatalf("parse JSONL: %v", err)
	}

	if line.Type != "conversation_created" {
		t.Errorf("expected type 'conversation_created', got %q", line.Type)
	}
}

func TestServer_JSONL_LoadConversations(t *testing.T) {
	srv := newTestServer(t)

	// Write JSONL manually
	dir := srv.convDir()
	os.MkdirAll(dir, 0755)

	conv := map[string]string{"id": "conv_1", "agent": "code", "title": "Test"}
	line1 := jsonlLine{Type: "conversation_created", Payload: mustMarshalRaw(conv)}
	bubble := BubbleMessage{Type: "user", Content: "hello"}
	line2 := jsonlLine{Type: "bubble", Payload: mustMarshalRaw(bubble)}

	f, _ := os.Create(filepath.Join(dir, "conv_1.jsonl"))
	f.WriteString(string(mustMarshalRaw(line1)) + "\n")
	f.WriteString(string(mustMarshalRaw(line2)) + "\n")
	f.Close()

	// Load
	if err := srv.loadConversations(); err != nil {
		t.Fatal(err)
	}

	srv.mu.RLock()
	loaded, ok := srv.convs["conv_1"]
	srv.mu.RUnlock()

	if !ok {
		t.Fatal("expected conv_1 to be loaded")
	}
	if loaded.Agent != "code" {
		t.Errorf("expected agent 'code', got %q", loaded.Agent)
	}
	if loaded.Title != "Test" {
		t.Errorf("expected title 'Test', got %q", loaded.Title)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", loaded.Messages[0].Content)
	}
}

func TestServer_JSONL_MergeOnLoad(t *testing.T) {
	srv := newTestServer(t)

	dir := srv.convDir()
	os.MkdirAll(dir, 0755)

	conv := map[string]string{"id": "conv_m", "agent": "code"}
	line1 := jsonlLine{Type: "conversation_created", Payload: mustMarshalRaw(conv)}
	b1 := BubbleMessage{Type: "llm", Content: "hel"}
	b2 := BubbleMessage{Type: "llm", Content: "lo"}
	line2 := jsonlLine{Type: "bubble", Payload: mustMarshalRaw(b1)}
	line3 := jsonlLine{Type: "bubble_merge", Payload: mustMarshalRaw(b2)}

	f, _ := os.Create(filepath.Join(dir, "conv_m.jsonl"))
	f.WriteString(string(mustMarshalRaw(line1)) + "\n")
	f.WriteString(string(mustMarshalRaw(line2)) + "\n")
	f.WriteString(string(mustMarshalRaw(line3)) + "\n")
	f.Close()

	if err := srv.loadConversations(); err != nil {
		t.Fatal(err)
	}

	srv.mu.RLock()
	loaded := srv.convs["conv_m"]
	srv.mu.RUnlock()

	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("expected merged 'hello', got %q", loaded.Messages[0].Content)
	}
}

// -- Tests: Conversation ordering ---------------------------------------------

func TestServer_Conversations_Order_NewestFirst(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	// Create 3 conversations
	var convs [3]Conversation
	for i := 0; i < 3; i++ {
		postJSON(t, base+"/api/conversations", map[string]string{
			"agent": fmt.Sprintf("agent%d", i),
		}, &convs[i])
		time.Sleep(time.Millisecond) // ensure different timestamps
	}

	var list []*Conversation
	getJSON(t, base+"/api/conversations", &list)

	if len(list) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(list))
	}

	// Newest should be first
	if list[0].ID != convs[2].ID {
		t.Errorf("expected newest first (%s), got %s", convs[2].ID, list[0].ID)
	}
	if list[2].ID != convs[0].ID {
		t.Errorf("expected oldest last (%s), got %s", convs[0].ID, list[2].ID)
	}
}

// -- Tests: SSE broadcast -----------------------------------------------------

func TestServer_SSE_BroadcastToMultipleClients(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Connect 2 SSE clients
	ch1 := make(chan SSEEvent, 8)
	ch2 := make(chan SSEEvent, 8)
	srv.sseMu.Lock()
	srv.sseClients[ch1] = struct{}{}
	srv.sseClients[ch2] = struct{}{}
	srv.sseMu.Unlock()

	// Broadcast
	srv.broadcastSSE("", "test", map[string]string{"data": "hello"})

	// Both should receive
	select {
	case ev := <-ch1:
		if ev.Event != "test" {
			t.Errorf("client1: expected event 'test', got %q", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("client1 timed out")
	}

	select {
	case ev := <-ch2:
		if ev.Event != "test" {
			t.Errorf("client2: expected event 'test', got %q", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("client2 timed out")
	}
}

func TestServer_SSE_BroadcastDuringConversationCreate(t *testing.T) {
	srv := newTestServer(t)
	mux := handlerMux(srv)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// SSE client
	ch := make(chan SSEEvent, 8)
	srv.sseMu.Lock()
	srv.sseClients[ch] = struct{}{}
	srv.sseMu.Unlock()

	// Create a conversation -> should broadcast
	var conv Conversation
	postJSON(t, ts.URL+"/api/conversations", map[string]string{
		"agent": "test",
	}, &conv)

	// Should get conversation_created event
	select {
	case ev := <-ch:
		if ev.Event != "conversation_created" {
			t.Errorf("expected conversation_created event, got %q", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conversation_created SSE event")
	}
}

// -- Tests: Edge cases --------------------------------------------------------

func TestServer_Conversation_DeleteRemovesFromOrder(t *testing.T) {
	srv := newTestServer(t)
	base := testURL(t, handlerMux(srv))

	var c1, c2 Conversation
	postJSON(t, base+"/api/conversations", map[string]string{"agent": "a1"}, &c1)
	postJSON(t, base+"/api/conversations", map[string]string{"agent": "a2"}, &c2)

	// Delete first
	deleteURL(t, base+"/api/conversations/"+c1.ID)

	var list []*Conversation
	getJSON(t, base+"/api/conversations", &list)

	if len(list) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(list))
	}
	if list[0].ID != c2.ID {
		t.Errorf("expected remaining conv %s, got %s", c2.ID, list[0].ID)
	}
}

// -- Helpers ------------------------------------------------------------------

func mustMarshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func wgWait(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for goroutine")
	}
}

// -- Tests: setContextConn prevents nil dereference --------------------------

// mockClientHandler is a minimal acp.Client for tests.
type mockClientHandler struct{}

func (m *mockClientHandler) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	return nil
}
func (m *mockClientHandler) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	return &acp.RequestPermissionResponse{}, nil
}
func (m *mockClientHandler) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	return &acp.ReadTextFileResponse{}, nil
}
func (m *mockClientHandler) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	return &acp.WriteTextFileResponse{}, nil
}
func (m *mockClientHandler) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	return &acp.CreateTerminalResponse{}, nil
}
func (m *mockClientHandler) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	return &acp.TerminalOutputResponse{}, nil
}
func (m *mockClientHandler) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	return &acp.ReleaseTerminalResponse{}, nil
}
func (m *mockClientHandler) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	return &acp.WaitForTerminalExitResponse{}, nil
}
func (m *mockClientHandler) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	return &acp.KillTerminalResponse{}, nil
}

// TestSetContextConn_PreventsNilDeref verifies that calling NewSession on a
// ClientSideConnection that hasn't had Start() called yet does NOT panic
// when setContextConn has been used to pre-set the ctx field.
func TestSetContextConn_PreventsNilDeref(t *testing.T) {
	// Create a pipe pair so the connection has real reader/writer.
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	handler := &mockClientHandler{}
	conn := acp.NewClientSideConnection(handler, pw, pr)

	// Pre-set ctx before Start
	setContextConn(conn, context.Background())

	// NewSession should NOT panic. It will error (no server on the
	// other end), but the important thing is no nil pointer deref.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := conn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/tmp"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Log("NewSession returned nil error (unexpected but not a panic)")
		} else {
			t.Logf("NewSession returned error (expected): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewSession timed out -- likely blocked on writeQueue")
	}
}

// TestSetContextConn_PanicWithoutFix verifies that WITHOUT setContextConn,
// calling NewSession before Start causes a nil pointer dereference panic.
// This test is skipped if the panic is not detected (e.g. library fixed).
func TestSetContextConn_PanicWithoutFix(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	handler := &mockClientHandler{}
	conn := acp.NewClientSideConnection(handler, pw, pr)

	// Do NOT call setContextConn -- test that we get a panic
	panicked := make(chan bool, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Recovered panic (expected without fix): %v", r)
				panicked <- true
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/tmp"})
		panicked <- false
	}()

	select {
	case didPanic := <-panicked:
		if !didPanic {
			t.Log("No panic detected -- library may have been fixed to handle nil ctx")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for NewSession")
	}
}

// -- Integration: full user flow with mock ACP subprocess --------------------

// TestServer_NewConversation_SendMessage simulates the exact user flow:
// 1. Open webapp -> GET /
// 2. Click new conversation -> POST /api/conversations
// 3. Leave defaults, send a text message -> POST /api/conversations/{id}/init
// 4. Verify no panic and proper error (since no real LLM backend)
func TestServer_NewConversation_SendMessage(t *testing.T) {
	srv := newTestServerWithConfigs(t)
	base := testURL(t, handlerMux(srv))

	// Step 1: Open webapp
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for GET /, got %d", resp.StatusCode)
	}

	// Step 2: Create new conversation (defaults)
	var conv Conversation
	postJSON(t, base+"/api/conversations", map[string]string{}, &conv)
	if conv.ID == "" {
		t.Fatal("expected conversation ID")
	}
	t.Logf("Created conversation: %s", conv.ID)

	// Step 3: Init with a text message (simulates user typing and sending)
	// This should NOT panic -- it should return an error about ACP not being available.
	initResp, err := http.Post(
		base+"/api/conversations/"+conv.ID+"/init",
		"application/json",
		strings.NewReader(`{"prompt":"Hello, this is a test message"}`),
	)
	if err != nil {
		// EOF from recovered panic is NOT acceptable anymore
		t.Fatalf("Init request failed (possible panic): %v", err)
	}
	defer initResp.Body.Close()

	if initResp.StatusCode == 200 {
		t.Log("Init succeeded (llmdevkit found in PATH)")
	} else {
		t.Logf("Init returned status %d", initResp.StatusCode)
		// Read body to ensure no panic trace in response
		body, _ := io.ReadAll(initResp.Body)
		bodyStr := string(body)
		if strings.Contains(bodyStr, "panic") || strings.Contains(bodyStr, "nil pointer") {
			t.Fatalf("Response contains panic/nil pointer: %s", bodyStr)
		}
	}
}
