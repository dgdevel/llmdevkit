package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/ironpark/go-acp"
)

// ---------------------------------------------------------------------------
// MockLLM - a scriptable, OpenAI-compatible HTTP server for testing.
//
// Enqueue steps (text, tool calls, or combinations), then the server
// consumes them in FIFO order, one step per /chat/completions call.
// ---------------------------------------------------------------------------

// MockLLMStep is a single scripted response.
type MockLLMStep struct {
	Text         string
	ToolCalls    []MockToolCall
	FinishReason string // auto-detected if empty
}

// MockToolCall is a single tool call the LLM will request.
type MockToolCall struct {
	ID        string
	Function  string
	Arguments json.RawMessage
}

// MockLLM is a scriptable mock OpenAI chat/completions server.
type MockLLM struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	steps  []MockLLMStep
	cursor int

	callLog           []mockLLMCallLog
	toolCallIDCounter int64
}

type mockLLMCallLog struct {
	Model    string
	Messages []mockLLMMessage
	Body     json.RawMessage
}

type mockLLMMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []mockLLMToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type mockLLMToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type mockLLMChoice struct {
	Message struct {
		Role      string            `json:"role"`
		Content   string            `json:"content"`
		ToolCalls []mockLLMToolCall `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type mockLLMResponse struct {
	Choices []mockLLMChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// NewMockLLM creates a new mock LLM server.
func NewMockLLM(t *testing.T) *MockLLM {
	m := &MockLLM{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", m.handleChatCompletions)
	m.server = httptest.NewServer(mux)
	return m
}

// Close shuts down the mock server.
func (m *MockLLM) Close() { m.server.Close() }

// URL returns the base URL of the mock LLM server.
func (m *MockLLM) URL() string { return m.server.URL }

// EnqueueText adds a step that responds with plain text content.
func (m *MockLLM) EnqueueText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, MockLLMStep{Text: text})
}

// EnqueueTextf adds a formatted text step.
func (m *MockLLM) EnqueueTextf(format string, args ...any) {
	m.EnqueueText(fmt.Sprintf(format, args...))
}

// EnqueueToolCall adds a step that requests one tool call.
func (m *MockLLM) EnqueueToolCall(id, fn string, arguments string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		id = fmt.Sprintf("call_%d", atomic.AddInt64(&m.toolCallIDCounter, 1))
	}
	m.steps = append(m.steps, MockLLMStep{
		ToolCalls: []MockToolCall{
			{ID: id, Function: fn, Arguments: json.RawMessage(arguments)},
		},
	})
}

// EnqueueToolCalls adds a step with multiple simultaneous tool calls.
func (m *MockLLM) EnqueueToolCalls(calls ...MockToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", atomic.AddInt64(&m.toolCallIDCounter, 1))
		}
	}
	m.steps = append(m.steps, MockLLMStep{ToolCalls: calls})
}

// EnqueueTextAndToolCalls adds a step with both text and tool calls.
func (m *MockLLM) EnqueueTextAndToolCalls(text string, calls ...MockToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d", atomic.AddInt64(&m.toolCallIDCounter, 1))
		}
	}
	m.steps = append(m.steps, MockLLMStep{Text: text, ToolCalls: calls})
}

// EnqueueStep adds an arbitrary step.
func (m *MockLLM) EnqueueStep(step MockLLMStep) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, step)
}

// StepsQueued returns how many steps are still unconsumed.
func (m *MockLLM) StepsQueued() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps) - m.cursor
}

// CallCount returns the number of /chat/completions calls received.
func (m *MockLLM) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.callLog)
}

// CallLogMessages returns messages from the Nth call (0-indexed).
func (m *MockLLM) CallLogMessages(n int) []mockLLMMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n >= len(m.callLog) {
		return nil
	}
	return m.callLog[n].Messages
}

// WaitForCallCount waits up to timeout for at least n calls.
func (m *MockLLM) WaitForCallCount(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.CallCount() >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Reset clears all queued steps and call history.
func (m *MockLLM) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = nil
	m.cursor = 0
	m.callLog = nil
}

// handleChatCompletions implements the HTTP endpoint.
func (m *MockLLM) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Model    string           `json:"model"`
		Messages []mockLLMMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.callLog = append(m.callLog, mockLLMCallLog{
		Model:    req.Model,
		Messages: req.Messages,
		Body:     json.RawMessage(body),
	})

	if m.cursor >= len(m.steps) {
		m.mu.Unlock()
		writeMockResponse(w, mockLLMResponse{
			Choices: []mockLLMChoice{{
				Message: struct {
					Role      string            `json:"role"`
					Content   string            `json:"content"`
					ToolCalls []mockLLMToolCall `json:"tool_calls"`
				}{Role: "assistant", Content: "mock: no more scripted steps"},
				FinishReason: "stop",
			}},
		})
		return
	}

	step := m.steps[m.cursor]
	m.cursor++
	m.mu.Unlock()

	writeMockResponse(w, buildMockResponse(step))
}

func buildMockResponse(step MockLLMStep) mockLLMResponse {
	var toolCalls []mockLLMToolCall
	for _, tc := range step.ToolCalls {
		call := mockLLMToolCall{ID: tc.ID, Type: "function"}
		call.Function.Name = tc.Function
		call.Function.Arguments = tc.Arguments
		toolCalls = append(toolCalls, call)
	}

	finishReason := step.FinishReason
	if finishReason == "" {
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}

	return mockLLMResponse{
		Choices: []mockLLMChoice{{
			Message: struct {
				Role      string            `json:"role"`
				Content   string            `json:"content"`
				ToolCalls []mockLLMToolCall `json:"tool_calls"`
			}{Role: "assistant", Content: step.Text, ToolCalls: toolCalls},
			FinishReason: finishReason,
		}},
	}
}

func writeMockResponse(w http.ResponseWriter, resp mockLLMResponse) {
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(resp)
	w.Write(data)
}

// ---------------------------------------------------------------------------
// RecordingClient - implements acp.Client, recording every call.
// ---------------------------------------------------------------------------

// RecordingClient records all client-side ACP calls for assertions.
type RecordingClient struct {
	mu sync.Mutex

	SessionUpdates []acp.SessionNotification
	Permissions    []acp.RequestPermissionRequest
	FileReads      []acp.ReadTextFileRequest
	FileWrites     []acp.WriteTextFileRequest
	Terminals      []acp.CreateTerminalRequest

	PermissionHandler func(*acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error)
	ReadFileHandler   func(*acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error)
	WriteFileHandler  func(*acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error)
}

func (c *RecordingClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	c.mu.Lock()
	c.SessionUpdates = append(c.SessionUpdates, *params)
	c.mu.Unlock()
	return nil
}

func (c *RecordingClient) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.Permissions = append(c.Permissions, *params)
	c.mu.Unlock()
	if c.PermissionHandler != nil {
		return c.PermissionHandler(params)
	}
	return &acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionID("allow")),
	}, nil
}

func (c *RecordingClient) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	c.mu.Lock()
	c.FileReads = append(c.FileReads, *params)
	c.mu.Unlock()
	if c.ReadFileHandler != nil {
		return c.ReadFileHandler(params)
	}
	return &acp.ReadTextFileResponse{Content: "mock file content for " + params.Path}, nil
}

func (c *RecordingClient) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	c.mu.Lock()
	c.FileWrites = append(c.FileWrites, *params)
	c.mu.Unlock()
	if c.WriteFileHandler != nil {
		return c.WriteFileHandler(params)
	}
	return &acp.WriteTextFileResponse{}, nil
}

func (c *RecordingClient) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	c.mu.Lock()
	c.Terminals = append(c.Terminals, *params)
	c.mu.Unlock()
	return &acp.CreateTerminalResponse{TerminalID: "mock-term-1"}, nil
}

func (c *RecordingClient) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	return &acp.TerminalOutputResponse{Output: "mock terminal output"}, nil
}

func (c *RecordingClient) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	return &acp.ReleaseTerminalResponse{}, nil
}

func (c *RecordingClient) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	code := int64(0)
	return &acp.WaitForTerminalExitResponse{ExitCode: &code}, nil
}

func (c *RecordingClient) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	return &acp.KillTerminalResponse{}, nil
}

// GetSessionUpdates returns a snapshot of all recorded session updates.
func (c *RecordingClient) GetSessionUpdates() []acp.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]acp.SessionNotification, len(c.SessionUpdates))
	copy(out, c.SessionUpdates)
	return out
}

// FindTextUpdates collects all text from agent message chunks.
func (c *RecordingClient) FindTextUpdates() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var texts []string
	for _, u := range c.SessionUpdates {
		if chunk, ok := u.Update.AsAgentMessageChunk(); ok {
			if text, ok := chunk.Content.AsText(); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

// FindThoughtUpdates collects all text from agent thought chunks.
func (c *RecordingClient) FindThoughtUpdates() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var texts []string
	for _, u := range c.SessionUpdates {
		if chunk, ok := u.Update.AsAgentThoughtChunk(); ok {
			if text, ok := chunk.Content.AsText(); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

// FindToolCalls collects all tool call updates.
func (c *RecordingClient) FindToolCalls() []acp.ToolCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var calls []acp.ToolCall
	for _, u := range c.SessionUpdates {
		if tc, ok := u.Update.AsToolCall(); ok {
			calls = append(calls, tc.ToolCall)
		}
	}
	return calls
}

// FindToolCallUpdates collects all tool call update notifications.
func (c *RecordingClient) FindToolCallUpdates() []acp.ToolCallUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var updates []acp.ToolCallUpdate
	for _, u := range c.SessionUpdates {
		if tcu, ok := u.Update.AsToolCallUpdate(); ok {
			updates = append(updates, tcu.ToolCallUpdate)
		}
	}
	return updates
}

// ---------------------------------------------------------------------------
// TestConnection - bidirectional pipe (same as acp integration test pattern).
// ---------------------------------------------------------------------------

type testPipe struct {
	clientToAgent *io.PipeReader
	agentToClient *io.PipeReader
	clientWriter  *io.PipeWriter
	agentWriter   *io.PipeWriter
}

func newTestPipe() *testPipe {
	cr, aw := io.Pipe()
	ar, cw := io.Pipe()
	return &testPipe{
		clientToAgent: cr,
		agentToClient: ar,
		clientWriter:  cw,
		agentWriter:   aw,
	}
}

func (p *testPipe) Close() {
	p.clientWriter.Close()
	p.agentWriter.Close()
	p.clientToAgent.Close()
	p.agentToClient.Close()
}

// ---------------------------------------------------------------------------
// testLLMAgent - an acp.Agent backed by MockLLM, simulating llmdevkit-acp.
// ---------------------------------------------------------------------------

type testLLMAgent struct {
	mock    *MockLLM
	tools   map[string]func(ctx context.Context, args json.RawMessage) (string, error)
	client  acp.Client
	toolSeq int64
}

func (a *testLLMAgent) Initialize(ctx context.Context, params *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		AgentCapabilities: &acp.AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &acp.PromptCapabilities{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
			SessionCapabilities: &acp.SessionCapabilities{
				Fork:   &acp.SessionForkCapabilities{},
				Close:  &acp.SessionCloseCapabilities{},
				List:   &acp.SessionListCapabilities{},
				Resume: &acp.SessionResumeCapabilities{},
			},
		},
		AuthMethods: []acp.AuthMethod{},
		AgentInfo: &acp.Implementation{
			Name:    "test-llm-agent",
			Version: "0.0.1-test",
		},
	}, nil
}

func (a *testLLMAgent) Authenticate(ctx context.Context, params *acp.AuthenticateRequest) (*acp.AuthenticateResponse, error) {
	return &acp.AuthenticateResponse{}, nil
}

func (a *testLLMAgent) SetSessionMode(ctx context.Context, params *acp.SetSessionModeRequest) (*acp.SetSessionModeResponse, error) {
	return &acp.SetSessionModeResponse{}, nil
}

func (a *testLLMAgent) SetSessionConfigOption(ctx context.Context, params *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	return &acp.SetSessionConfigOptionResponse{}, nil
}

func (a *testLLMAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	return nil
}

func (a *testLLMAgent) Prompt(ctx context.Context, params *acp.PromptRequest) (*acp.PromptResponse, error) {
	stream := acp.NewSessionStream(a.client, params.SessionID)

	// Extract user text
	var userText string
	for _, block := range params.Prompt {
		if txt, ok := block.AsText(); ok {
			userText += txt.Text
		}
	}

	messages := []runnerMsg{{Role: "user", Content: userText}}

	for {
		resp, err := a.mock.CallLLM(ctx, messages)
		if err != nil {
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("mock LLM returned no choices")
		}

		choice := resp.Choices[0]
		content := choice.Message.Content

		// Stream text
		if content != "" {
			if err := stream.SendText(ctx, content); err != nil {
				return nil, err
			}
		}

		// No tool calls -> done
		if len(choice.Message.ToolCalls) == 0 || choice.FinishReason == "stop" {
			return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
		}

		// Process tool calls
		assistantMsg := runnerMsg{
			Role:      "assistant",
			Content:   content,
			ToolCalls: choice.Message.ToolCalls,
		}
		messages = append(messages, assistantMsg)

		for _, tc := range choice.Message.ToolCalls {
			a.toolSeq++
			toolID := acp.ToolCallID(fmt.Sprintf("tc_%d", a.toolSeq))
			_ = stream.StartToolCall(ctx, toolID, tc.Function.Name, acp.ToolKindOther)

			var result string
			fn, ok := a.tools[tc.Function.Name]
			if ok {
				result, err = fn(ctx, tc.Function.Arguments)
				if err != nil {
					result = fmt.Sprintf("error: %v", err)
					_ = stream.FailToolCall(ctx, toolID)
				} else {
					_ = stream.CompleteToolCall(ctx, toolID,
						acp.NewToolCallContentContent(acp.NewContentBlockText(result)))
				}
			} else {
				result = fmt.Sprintf("unknown tool: %s", tc.Function.Name)
				_ = stream.FailToolCall(ctx, toolID)
			}

			messages = append(messages, runnerMsg{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
}

// RegisterTool adds a tool handler to the test agent.
func (a *testLLMAgent) RegisterTool(name string, fn func(ctx context.Context, args json.RawMessage) (string, error)) {
	if a.tools == nil {
		a.tools = make(map[string]func(ctx context.Context, args json.RawMessage) (string, error))
	}
	a.tools[name] = fn
}

// runnerMsg mirrors the runner.ChatMessage shape (avoids import coupling).
type runnerMsg struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []mockLLMToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

// CallLLM sends a request to the mock LLM server.
func (m *MockLLM) CallLLM(ctx context.Context, messages []runnerMsg) (*mockLLMResponse, error) {
	type req struct {
		Model    string      `json:"model"`
		Messages []runnerMsg `json:"messages"`
	}
	body, err := json.Marshal(req{Model: "mock-model", Messages: messages})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.URL()+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mock LLM HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp mockLLMResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, err
	}
	return &chatResp, nil
}

// ---------------------------------------------------------------------------
// Harness - wires everything together for end-to-end ACP tests.
// ---------------------------------------------------------------------------

// TestHarness wires a MockLLM + testLLMAgent + RecordingClient + pipe connections.
type TestHarness struct {
	Mock       *MockLLM
	Client     *RecordingClient
	Agent      *testLLMAgent
	AgentConn  *acp.AgentSideConnection
	ClientConn *acp.ClientSideConnection
	pipe       *testPipe
	cancel     context.CancelFunc
}

// NewTestHarness creates a fully wired test harness.
func NewTestHarness(t *testing.T) *TestHarness {
	mock := NewMockLLM(t)
	pipe := newTestPipe()
	rc := &RecordingClient{}

	agent := &testLLMAgent{mock: mock}
	agentConn := acp.NewAgentSideConnection(agent, pipe.clientToAgent, pipe.clientWriter)
	clientConn := acp.NewClientSideConnection(rc, pipe.agentWriter, pipe.agentToClient)

	// Wire agent's client to the agent connection so SessionStream works.
	agent.client = agentConn.Client()

	_, cancel := context.WithCancel(context.Background())

	return &TestHarness{
		Mock:       mock,
		Client:     rc,
		Agent:      agent,
		AgentConn:  agentConn,
		ClientConn: clientConn,
		pipe:       pipe,
		cancel:     cancel,
	}
}

// Start starts both sides of the connection.
func (h *TestHarness) Start(ctx context.Context) {
	go func() { _ = h.AgentConn.Start(ctx) }()
	go func() { _ = h.ClientConn.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)
}

// Close shuts down everything.
func (h *TestHarness) Close() {
	if h.cancel != nil {
		h.cancel()
	}
	h.pipe.Close()
	h.Mock.Close()
}
