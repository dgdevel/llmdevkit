package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
	"llmdevkit/internal/llms"
	"llmdevkit/internal/mcps"
	"llmdevkit/internal/tools"

	acp "github.com/ironpark/go-acp"
)

// -- Data types --------------------------------------------------------------

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
	LLM          string          `json:"llm,omitempty"`
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
	LastPromptTokens  int `json:"last_prompt_tokens,omitempty"`

	PromptCancel context.CancelFunc `json:"-"` // cancel the running prompt context
	
	// File change tracking during a prompt run (transient, not persisted)
	FileChanges []FileChange `json:"-"`
	}
	
	// FileChange tracks a single file modification during a prompt run.
	type FileChange struct {
	ToolName  string `json:"tool_name"`
	Path      string `json:"path"`
	DestPath  string `json:"dest_path,omitempty"` // for mv
	DiffLines int    `json:"diff_lines,omitempty"`
	Response  string `json:"-"` // tool response content for parsing
	}
	
	type jsonlLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	}

// -- Server state ------------------------------------------------------------

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
	toolDefsCache map[string][]ToolDefInfo // agent name -> cached tool defs from ACP

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

// Suppress unused import for tools (used in server_sse.go)
var _ = tools.RootDir
