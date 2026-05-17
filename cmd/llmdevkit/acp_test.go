package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	acp "github.com/ironpark/go-acp"
)

// ---------------------------------------------------------------------------
// MockLLM unit tests
// ---------------------------------------------------------------------------

func TestMockLLM_TextResponse(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueText("Hello from mock!")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from mock!" {
		t.Errorf("expected 'Hello from mock!', got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", resp.Choices[0].FinishReason)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", mock.CallCount())
	}
}

func TestMockLLM_ToolCallResponse(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueToolCall("call_1", "read_file", `{"path":"/tmp/x"}`)

	ctx := context.Background()
	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "read the file"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Errorf("expected function 'read_file', got %q", tc.Function.Name)
	}
	if tc.ID != "call_1" {
		t.Errorf("expected id 'call_1', got %q", tc.ID)
	}
}

func TestMockLLM_MultiStepSequence(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueToolCall("call_1", "read_file", `{"path":"/tmp/a"}`)
	mock.EnqueueText("Here's what I found in the file.")

	ctx := context.Background()

	// First call → tool call
	resp1, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "read file"}})
	if err != nil {
		t.Fatalf("Call 1 failed: %v", err)
	}
	if len(resp1.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("expected tool call in first response")
	}

	// Second call → text
	resp2, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "read file"}, {Role: "tool", Content: "file content", ToolCallID: "call_1"}})
	if err != nil {
		t.Fatalf("Call 2 failed: %v", err)
	}
	if resp2.Choices[0].Message.Content != "Here's what I found in the file." {
		t.Errorf("unexpected second response: %q", resp2.Choices[0].Message.Content)
	}
	if mock.CallCount() != 2 {
		t.Errorf("expected 2 calls, got %d", mock.CallCount())
	}
}

func TestMockLLM_MultipleToolCalls(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueToolCalls(
		MockToolCall{ID: "c1", Function: "read_file", Arguments: json.RawMessage(`{"path":"/a"}`)},
		MockToolCall{ID: "c2", Function: "read_file", Arguments: json.RawMessage(`{"path":"/b"}`)},
	)

	ctx := context.Background()
	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "read both"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.Choices[0].Message.ToolCalls))
	}
}

func TestMockLLM_ExhaustedSteps(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueText("first")

	ctx := context.Background()
	_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "hi"}})

	// Second call with no more steps → default response
	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "hi again"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if !strings.Contains(resp.Choices[0].Message.Content, "no more scripted steps") {
		t.Errorf("expected default message, got %q", resp.Choices[0].Message.Content)
	}
}

func TestMockLLM_CallLog(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueText("hi")
	mock.EnqueueText("hello")

	ctx := context.Background()
	_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "msg1"}})
	_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "msg2"}, {Role: "assistant", Content: "hi"}})

	if mock.CallCount() != 2 {
		t.Fatalf("expected 2 calls, got %d", mock.CallCount())
	}

	msgs1 := mock.CallLogMessages(0)
	if len(msgs1) != 1 || msgs1[0].Content != "msg1" {
		t.Errorf("call 0 messages unexpected: %+v", msgs1)
	}

	msgs2 := mock.CallLogMessages(1)
	if len(msgs2) != 2 {
		t.Fatalf("expected 2 messages in call 1, got %d", len(msgs2))
	}
	if msgs2[0].Role != "user" || msgs2[0].Content != "msg2" {
		t.Errorf("unexpected msg[0]: %+v", msgs2[0])
	}
	if msgs2[1].Role != "assistant" || msgs2[1].Content != "hi" {
		t.Errorf("unexpected msg[1]: %+v", msgs2[1])
	}
}

func TestMockLLM_Reset(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueText("first")
	ctx := context.Background()
	_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "hi"}})

	if mock.CallCount() != 1 {
		t.Fatalf("expected 1 call before reset, got %d", mock.CallCount())
	}

	mock.Reset()
	if mock.CallCount() != 0 {
		t.Errorf("expected 0 calls after reset, got %d", mock.CallCount())
	}
	if mock.StepsQueued() != 0 {
		t.Errorf("expected 0 steps after reset, got %d", mock.StepsQueued())
	}
}

func TestMockLLM_WaitForCallCount(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueText("a")
	mock.EnqueueText("b")

	ctx := context.Background()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "1"}})
		_, _ = mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "2"}})
	}()

	if !mock.WaitForCallCount(2, 2*time.Second) {
		t.Fatal("WaitForCallCount timed out")
	}
}

func TestMockLLM_TextAndToolCalls(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueTextAndToolCalls("Let me look that up.",
		MockToolCall{ID: "c1", Function: "search", Arguments: json.RawMessage(`{"query":"test"}`)},
	)

	ctx := context.Background()
	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "search for test"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	choice := resp.Choices[0]
	if choice.Message.Content != "Let me look that up." {
		t.Errorf("unexpected text: %q", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
	}
}

func TestMockLLM_CustomFinishReason(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()

	mock.EnqueueStep(MockLLMStep{Text: "length exceeded", FinishReason: "length"})

	ctx := context.Background()
	resp, err := mock.CallLLM(ctx, []runnerMsg{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if resp.Choices[0].FinishReason != "length" {
		t.Errorf("expected 'length', got %q", resp.Choices[0].FinishReason)
	}
}

// ---------------------------------------------------------------------------
// RecordingClient unit tests
// ---------------------------------------------------------------------------

func TestRecordingClient_TextUpdates(t *testing.T) {
	rc := &RecordingClient{}

	ctx := context.Background()
	_ = rc.SessionUpdate(ctx, &acp.SessionNotification{
		SessionID: "s1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("hello"), ""),
	})
	_ = rc.SessionUpdate(ctx, &acp.SessionNotification{
		SessionID: "s1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" world"), ""),
	})

	texts := rc.FindTextUpdates()
	if len(texts) != 2 {
		t.Fatalf("expected 2 text updates, got %d", len(texts))
	}
	if texts[0] != "hello" || texts[1] != " world" {
		t.Errorf("unexpected texts: %v", texts)
	}
}

func TestRecordingClient_ThoughtUpdates(t *testing.T) {
	rc := &RecordingClient{}
	ctx := context.Background()

	_ = rc.SessionUpdate(ctx, &acp.SessionNotification{
		SessionID: "s1",
		Update:    acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("thinking..."), ""),
	})

	thoughts := rc.FindThoughtUpdates()
	if len(thoughts) != 1 || thoughts[0] != "thinking..." {
		t.Errorf("unexpected thoughts: %v", thoughts)
	}
}

func TestRecordingClient_PermissionHandler(t *testing.T) {
	rc := &RecordingClient{}
	rc.PermissionHandler = func(req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionID("reject")),
		}, nil
	}

	ctx := context.Background()
	resp, err := rc.RequestPermission(ctx, &acp.RequestPermissionRequest{
		SessionID: "s1",
		ToolCall: acp.ToolCallUpdate{ToolCallID: "tc1", Title: "rm file"},
	})
	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}
	selected, ok := resp.Outcome.AsSelected()
	if !ok || string(selected.OptionID) != "reject" {
		t.Errorf("expected reject, got %+v", resp.Outcome)
	}
}

// ---------------------------------------------------------------------------
// End-to-end ACP tests via TestHarness
// ---------------------------------------------------------------------------

func TestE2E_Initialize(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)

	resp, err := h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{
			FS: &acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if resp.ProtocolVersion != acp.ProtocolVersion(acp.CurrentProtocolVersion) {
		t.Errorf("expected protocol version %d, got %d", acp.CurrentProtocolVersion, resp.ProtocolVersion)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "test-llm-agent" {
		t.Errorf("unexpected agent info: %+v", resp.AgentInfo)
	}
	if resp.AgentCapabilities == nil || !resp.AgentCapabilities.LoadSession {
		t.Error("expected LoadSession capability")
	}
}

func TestE2E_NewSession(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	// Without SessionStore, the agent needs to implement SessionCreator.
	// Our testLLMAgent doesn't, so it'll return method not found.
	// Let's test with a SessionStore instead.
	pipe := newTestPipe()
	defer pipe.Close()
	rc := &RecordingClient{} // re-use existing rc

	agent := h.Agent
	store := acp.NewMemoryStore[string]()
	agentConn := acp.NewAgentSideConnection(agent, pipe.clientToAgent, pipe.clientWriter,
		acp.WithSessionStore(store, func(ctx context.Context, params *acp.NewSessionRequest) (acp.SessionID, string, error) {
			return acp.GenerateSessionID(), "test-session-data", nil
		}),
	)
	clientConn := acp.NewClientSideConnection(rc, pipe.agentWriter, pipe.agentToClient)
	agent.client = agentConn.Client()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = agentConn.Start(ctx2) }()
	go func() { _ = clientConn.Start(ctx2) }()
	time.Sleep(80 * time.Millisecond)

	resp, err := clientConn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd:        "/test",
		MCPServers: []acp.MCPServer{},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if resp.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if !strings.HasPrefix(string(resp.SessionID), "session_") {
		t.Errorf("expected session ID to start with 'session_', got %q", resp.SessionID)
	}
}

func TestE2E_SimpleTextPrompt(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	h.Mock.EnqueueText("Hello! I can help you with that.")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	// Initialize first
	_, err := h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Send prompt
	resp, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("test-session"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Hello, how are you?")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("expected stop_reason 'end_turn', got %q", resp.StopReason)
	}

	// Verify client received text update
	time.Sleep(200 * time.Millisecond)
	texts := h.Client.FindTextUpdates()
	found := false
	for _, t := range texts {
		if t == "Hello! I can help you with that." {
			found = true
		}
	}
	if !found {
		t.Errorf("expected text update 'Hello! I can help you with that.', got %v", texts)
	}

	// Verify LLM was called once
	if h.Mock.CallCount() != 1 {
		t.Errorf("expected 1 LLM call, got %d", h.Mock.CallCount())
	}
}

func TestE2E_ToolCallLoop(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	// Script: tool call → tool result → final text
	h.Mock.EnqueueToolCall("call_1", "read_file", `{"path":"/tmp/test.txt"}`)
	h.Mock.EnqueueText("The file contains: mock file content for /tmp/test.txt")

	h.Agent.RegisterTool("read_file", func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		return "file content for " + params.Path, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	resp, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("test-session"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Read the file /tmp/test.txt")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Errorf("expected end_turn, got %q", resp.StopReason)
	}

	time.Sleep(200 * time.Millisecond)

	// Should have made 2 LLM calls (tool_call + final text)
	if !h.Mock.WaitForCallCount(2, 2*time.Second) {
		t.Errorf("expected 2 LLM calls, got %d", h.Mock.CallCount())
	}

	// Should have tool call notifications
	toolCalls := h.Client.FindToolCalls()
	if len(toolCalls) == 0 {
		t.Error("expected tool call notifications")
	}

	// Should have tool call updates (complete)
	updates := h.Client.FindToolCallUpdates()
	completed := 0
	for _, u := range updates {
		if u.Status != nil && *u.Status == acp.ToolCallStatusCompleted {
			completed++
		}
	}
	if completed == 0 {
		t.Error("expected at least one completed tool call update")
	}

	// Should have final text
	texts := h.Client.FindTextUpdates()
	found := false
	for _, t := range texts {
		if strings.Contains(t, "The file contains:") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected final text containing 'The file contains:', got %v", texts)
	}
}

func TestE2E_MultipleToolCalls(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	// Step 1: LLM requests two tool calls simultaneously
	h.Mock.EnqueueToolCalls(
		MockToolCall{ID: "c1", Function: "read_file", Arguments: json.RawMessage(`{"path":"/a.txt"}`)},
		MockToolCall{ID: "c2", Function: "read_file", Arguments: json.RawMessage(`{"path":"/b.txt"}`)},
	)
	// Step 2: LLM gives final answer
	h.Mock.EnqueueText("Both files have been read.")

	h.Agent.RegisterTool("read_file", func(ctx context.Context, args json.RawMessage) (string, error) {
		return "content", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	_, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("test-session"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Read both files")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// 2 LLM calls total
	if h.Mock.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.Mock.CallCount())
	}

	// Second call should have tool result messages
	msgs := h.Mock.CallLogMessages(1)
	hasToolMsg := false
	for _, m := range msgs {
		if m.Role == "tool" {
			hasToolMsg = true
		}
	}
	if !hasToolMsg {
		t.Error("expected tool result messages in second LLM call")
	}

	// Should have final text
	texts := h.Client.FindTextUpdates()
	found := false
	for _, t := range texts {
		if t == "Both files have been read." {
			found = true
		}
	}
	if !found {
		t.Errorf("expected final text, got %v", texts)
	}
}

func TestE2E_ToolCallFails(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	h.Mock.EnqueueToolCall("call_1", "bad_tool", `{"arg":"value"}`)
	h.Mock.EnqueueText("I encountered an error with bad_tool.")

	h.Agent.RegisterTool("bad_tool", func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", fmt.Errorf("tool exploded")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	_, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("test-session"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Use the bad tool")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Tool call update should show failed
	updates := h.Client.FindToolCallUpdates()
	failed := false
	for _, u := range updates {
		if u.Status != nil && *u.Status == acp.ToolCallStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Error("expected a failed tool call update")
	}

	// LLM should still get the error as tool result
	msgs := h.Mock.CallLogMessages(1)
	hasError := false
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "error:") {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected error in tool result message to LLM")
	}
}

func TestE2E_UnknownTool(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	h.Mock.EnqueueToolCall("call_1", "nonexistent_tool", `{}`)
	h.Mock.EnqueueText("That tool doesn't exist, sorry.")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	_, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("test-session"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Use nonexistent tool")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Tool should have failed
	updates := h.Client.FindToolCallUpdates()
	failed := false
	for _, u := range updates {
		if u.Status != nil && *u.Status == acp.ToolCallStatusFailed {
			failed = true
		}
	}
	if !failed {
		t.Error("expected failed status for unknown tool")
	}

	// LLM should receive "unknown tool" message
	msgs := h.Mock.CallLogMessages(1)
	found := false
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "unknown tool") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'unknown tool' in LLM tool result messages")
	}
}

func TestE2E_Authenticate(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	resp, err := h.ClientConn.Authenticate(ctx, &acp.AuthenticateRequest{
		MethodID: "oauth",
	})
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

func TestE2E_SetSessionMode(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	resp, err := h.ClientConn.SetSessionMode(ctx, &acp.SetSessionModeRequest{
		SessionID: acp.SessionID("s1"),
		ModeID:    acp.SessionModeID("code"),
	})
	if err != nil {
		t.Fatalf("SetSessionMode failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

func TestE2E_Cancel(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	err := h.ClientConn.Cancel(ctx, &acp.CancelNotification{
		SessionID: acp.SessionID("s1"),
	})
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
}

func TestE2E_ClientSidePermissions(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	// Agent requests permission from client
	resp, err := h.AgentConn.Client().RequestPermission(ctx, &acp.RequestPermissionRequest{
		SessionID: acp.SessionID("s1"),
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: acp.ToolCallID("tc_1"),
			Title:      "Delete file",
			Kind:       pointerToToolKind(acp.ToolKindDelete),
			Status:     pointerToToolCallStatus(acp.ToolCallStatusPending),
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow", OptionID: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionID: "reject"},
		},
	})
	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}

	selected, ok := resp.Outcome.AsSelected()
	if !ok {
		t.Fatal("expected selected outcome")
	}
	if string(selected.OptionID) != "allow" {
		t.Errorf("expected 'allow', got %q", selected.OptionID)
	}

	// RecordingClient should have logged it
	if len(h.Client.Permissions) != 1 {
		t.Errorf("expected 1 permission request recorded, got %d", len(h.Client.Permissions))
	}
}

func TestE2E_ClientSideFileOps(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	ctx := context.Background()
	h.Start(ctx)

	// Agent reads a file
	readResp, err := h.AgentConn.Client().ReadTextFile(ctx, &acp.ReadTextFileRequest{
		SessionID: acp.SessionID("s1"),
		Path:      "/tmp/test.txt",
	})
	if err != nil {
		t.Fatalf("ReadTextFile failed: %v", err)
	}
	if !strings.Contains(readResp.Content, "/tmp/test.txt") {
		t.Errorf("unexpected content: %q", readResp.Content)
	}

	// Agent writes a file
	writeResp, err := h.AgentConn.Client().WriteTextFile(ctx, &acp.WriteTextFileRequest{
		SessionID: acp.SessionID("s1"),
		Path:      "/tmp/output.txt",
		Content:   "hello world",
	})
	if err != nil {
		t.Fatalf("WriteTextFile failed: %v", err)
	}
	if writeResp == nil {
		t.Error("expected non-nil write response")
	}

	// RecordingClient should have both
	if len(h.Client.FileReads) != 1 {
		t.Errorf("expected 1 file read, got %d", len(h.Client.FileReads))
	}
	if len(h.Client.FileWrites) != 1 {
		t.Errorf("expected 1 file write, got %d", len(h.Client.FileWrites))
	}
}

func TestE2E_ClientRejectsPermission(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()
	h.Client.PermissionHandler = func(req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionID("reject")),
		}, nil
	}

	ctx := context.Background()
	h.Start(ctx)

	resp, err := h.AgentConn.Client().RequestPermission(ctx, &acp.RequestPermissionRequest{
		SessionID: acp.SessionID("s1"),
		ToolCall: acp.ToolCallUpdate{
			ToolCallID: "tc_1",
			Title:      "Dangerous op",
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionID: "reject"},
		},
	})
	if err != nil {
		t.Fatalf("RequestPermission failed: %v", err)
	}
	selected, ok := resp.Outcome.AsSelected()
	if !ok || string(selected.OptionID) != "reject" {
		t.Errorf("expected reject, got %+v", resp.Outcome)
	}
}

func TestE2E_MultiTurnConversation(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	// Turn 1: simple text
	h.Mock.EnqueueText("I'll help you with that.")
	// Turn 2: tool call then text
	h.Mock.EnqueueToolCall("call_1", "search", `{"query":"golang testing"}`)
	h.Mock.EnqueueText("Here are the testing results.")

	h.Agent.RegisterTool("search", func(ctx context.Context, args json.RawMessage) (string, error) {
		return "found 10 results", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	// Turn 1
	resp1, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("s1"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Help me")},
	})
	if err != nil {
		t.Fatalf("Prompt 1 failed: %v", err)
	}
	if resp1.StopReason != acp.StopReasonEndTurn {
		t.Errorf("expected end_turn, got %q", resp1.StopReason)
	}

	// Turn 2
	resp2, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("s1"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Now search for golang testing")},
	})
	if err != nil {
		t.Fatalf("Prompt 2 failed: %v", err)
	}
	if resp2.StopReason != acp.StopReasonEndTurn {
		t.Errorf("expected end_turn, got %q", resp2.StopReason)
	}

	time.Sleep(200 * time.Millisecond)

	// Total: 3 LLM calls (1 for turn1, 2 for turn2)
	if h.Mock.CallCount() != 3 {
		t.Errorf("expected 3 LLM calls, got %d", h.Mock.CallCount())
	}
}

func TestE2E_SessionStreamNotifications(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	h.Mock.EnqueueText("Planning... then acting.")
	h.Mock.EnqueueText("Done!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Start(ctx)

	_, _ = h.ClientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})

	_, err := h.ClientConn.Prompt(ctx, &acp.PromptRequest{
		SessionID: acp.SessionID("s1"),
		Prompt:    []acp.ContentBlock{acp.NewContentBlockText("Do something")},
	})
	if err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Should have session updates with correct session ID
	updates := h.Client.GetSessionUpdates()
	for _, u := range updates {
		if u.SessionID != acp.SessionID("s1") {
			t.Errorf("expected session ID 's1', got %q", u.SessionID)
		}
	}
}

func TestE2E_SessionStoreIntegration(t *testing.T) {
	mock := NewMockLLM(t)
	defer mock.Close()
	pipe := newTestPipe()
	defer pipe.Close()
	rc := &RecordingClient{}

	agent := &testLLMAgent{mock: mock}
	store := acp.NewMemoryStore[string]()
	agentConn := acp.NewAgentSideConnection(agent, pipe.clientToAgent, pipe.clientWriter,
		acp.WithSessionStore(store, func(ctx context.Context, params *acp.NewSessionRequest) (acp.SessionID, string, error) {
			return "session_stored_123", "data", nil
		}),
	)
	clientConn := acp.NewClientSideConnection(rc, pipe.agentWriter, pipe.agentToClient)
	agent.client = agentConn.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = agentConn.Start(ctx) }()
	go func() { _ = clientConn.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)

	// New session
	newResp, err := clientConn.NewSession(ctx, &acp.NewSessionRequest{
		Cwd:        "/test",
		MCPServers: []acp.MCPServer{},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if newResp.SessionID != acp.SessionID("session_stored_123") {
		t.Errorf("expected 'session_stored_123', got %q", newResp.SessionID)
	}

	// Load session
	loadResp, err := clientConn.LoadSession(ctx, &acp.LoadSessionRequest{
		SessionID:  newResp.SessionID,
		Cwd:        "/test",
		MCPServers: []acp.MCPServer{},
	})
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}
	if loadResp == nil {
		t.Error("expected non-nil load response")
	}

	// List sessions
	listResp, err := clientConn.ListSessions(ctx, &acp.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(listResp.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(listResp.Sessions))
	}
}

func TestE2E_Middleware(t *testing.T) {
	pipe := newTestPipe()
	defer pipe.Close()

	var loggedMethods []string
	var recovered bool

	agent := &testLLMAgent{mock: NewMockLLM(t)}
	defer agent.mock.Close()

	agentConn := acp.NewAgentSideConnection(agent, pipe.clientToAgent, pipe.clientWriter,
		acp.WithMiddleware(acp.LoggingMiddleware(func(format string, args ...any) {
			loggedMethods = append(loggedMethods, fmt.Sprintf(format, args...))
		})),
		acp.WithMiddleware(acp.RecoveryMiddleware()),
	)

	// Create a panicking agent for recovery test
	panicAgent := &panickingAgent{}
	agentConn2 := acp.NewAgentSideConnection(panicAgent, nil, nil,
		acp.WithMiddleware(acp.RecoveryMiddleware()),
	)
	_ = agentConn2

	rc := &RecordingClient{}
	clientConn := acp.NewClientSideConnection(rc, pipe.agentWriter, pipe.agentToClient)
	agent.client = agentConn.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = agentConn.Start(ctx) }()
	go func() { _ = clientConn.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)

	_, err := clientConn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Check logging middleware captured the method
	time.Sleep(100 * time.Millisecond)
	found := false
	for _, m := range loggedMethods {
		if strings.Contains(m, "initialize") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'initialize' in log, got %v", loggedMethods)
	}
	_ = recovered
}

func TestE2E_ConnectionClose(t *testing.T) {
	pipe := newTestPipe()
	defer pipe.Close()

	agent := &testLLMAgent{mock: NewMockLLM(t)}
	defer agent.mock.Close()

	agentConn := acp.NewAgentSideConnection(agent, pipe.clientToAgent, pipe.clientWriter)
	rc := &RecordingClient{}
	clientConn := acp.NewClientSideConnection(rc, pipe.agentWriter, pipe.agentToClient)
	agent.client = agentConn.Client()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = agentConn.Start(ctx) }()
	go func() { _ = clientConn.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Close should not panic
	cancel()
	agentConn.Close()
	clientConn.Close()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// panickingAgent panics on Initialize to test RecoveryMiddleware.
type panickingAgent struct{}

func (a *panickingAgent) Initialize(ctx context.Context, params *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	panic("intentional panic for testing")
}
func (a *panickingAgent) Authenticate(ctx context.Context, params *acp.AuthenticateRequest) (*acp.AuthenticateResponse, error) {
	return &acp.AuthenticateResponse{}, nil
}
func (a *panickingAgent) SetSessionMode(ctx context.Context, params *acp.SetSessionModeRequest) (*acp.SetSessionModeResponse, error) {
	return &acp.SetSessionModeResponse{}, nil
}
func (a *panickingAgent) SetSessionConfigOption(ctx context.Context, params *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	return &acp.SetSessionConfigOptionResponse{}, nil
}
func (a *panickingAgent) Prompt(ctx context.Context, params *acp.PromptRequest) (*acp.PromptResponse, error) {
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}
func (a *panickingAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	return nil
}

func pointerToToolKind(k acp.ToolKind) *acp.ToolKind    { return &k }
func pointerToToolCallStatus(s acp.ToolCallStatus) *acp.ToolCallStatus { return &s }
