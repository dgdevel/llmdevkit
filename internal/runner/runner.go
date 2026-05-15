package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
	"llmdevkit/internal/llms"
)

// ToolDef describes a tool available to the agent.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(ctx context.Context, args map[string]any) (string, error)
}

// ToolRegistry holds all tools available for a given agent configuration.
type ToolRegistry struct {
	tools map[string]*ToolDef
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*ToolDef)}
}

func (r *ToolRegistry) Add(def *ToolDef) {
	r.tools[def.Name] = def
}

func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) All() map[string]*ToolDef {
	return r.tools
}

// ListForOpenAI returns tools in OpenAI function-calling format.
func (r *ToolRegistry) ListForOpenAI() []map[string]any {
	var tools []map[string]any
	for _, t := range r.tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return tools
}

// CallTool invokes a tool by name with the given JSON arguments.
func (r *ToolRegistry) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	var parsed map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &parsed); err != nil {
			// Some LLMs return tool args as a JSON-encoded string instead of
			// a JSON object (double-encoded). Unwrap one layer and retry.
			var inner string
			if jsonErr := json.Unmarshal(args, &inner); jsonErr == nil {
				if unmarshalErr := json.Unmarshal([]byte(inner), &parsed); unmarshalErr == nil {
					goto done
				}
			}
			return "", fmt.Errorf("parse tool args: %w", err)
		}
	}
done:
	return t.Call(ctx, parsed)
}

// Runner executes an agent's LLM loop.
type Runner struct {
	llm          *llms.LLMConfig
	agent        *agents.AgentConfig
	registry     *ToolRegistry
	allAgents    *agents.Config // for agents_available / agent_invoke
	rootDir      string         // project directory, for reading AGENTS.md
	onText       func(text string)
	onToolStart  func(id, title, kind string, arguments json.RawMessage)
	onToolUpdate func(id, status, content string)
	onTokenStats func(TokenStats)
}

type Option func(*Runner)

func WithTextCallback(fn func(string)) Option {
	return func(r *Runner) { r.onText = fn }
}

func WithToolStartCallback(fn func(id, title, kind string, arguments json.RawMessage)) Option {
	return func(r *Runner) { r.onToolStart = fn }
}

func WithToolUpdateCallback(fn func(id, status, content string)) Option {
	return func(r *Runner) { r.onToolUpdate = fn }
}

func WithTokenStatsCallback(fn func(TokenStats)) Option {
	return func(r *Runner) { r.onTokenStats = fn }
}

func WithRootDir(dir string) Option {
	return func(r *Runner) { r.rootDir = dir }
}

func NewRunner(llmCfg *llms.LLMConfig, agentCfg *agents.AgentConfig, registry *ToolRegistry, allAgents *agents.Config, opts ...Option) *Runner {
	r := &Runner{
		llm:       llmCfg,
		agent:     agentCfg,
		registry:  registry,
		allAgents: allAgents,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ChatMessage represents a message in the OpenAI chat format.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []toolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []ChatMessage  `json:"messages"`
	Tools       []map[string]any `json:"tools,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// TokenStats holds accumulated token usage for a prompt turn.
type TokenStats struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	LLMCalls         int `json:"llm_calls"`
}

// RunPrompt executes a full prompt turn with the agent.
// promptText is the user's message. userPrompt is the original raw prompt (for %p substitution).
func (r *Runner) RunPrompt(ctx context.Context, messages []ChatMessage, userPrompt string) (string, error) {
	dlog := debuglog.For("runner")
	dlog.Log("RunPrompt msg_count=%d user_prompt_len=%d", len(messages), len(userPrompt))
	// Execute on_conversation_begin hooks (only on first message)
	if len(messages) <= 1 {
		if err := r.executeHooks(ctx, agents.HookConversationBegin, userPrompt); err != nil {
			return "", fmt.Errorf("hook conversation_begin: %w", err)
		}
	}

	// Execute on_turn_begin hooks
	if err := r.executeHooks(ctx, agents.HookTurnBegin, userPrompt); err != nil {
		return "", fmt.Errorf("hook turn_begin: %w", err)
	}

	model := r.llm.Model
	if model == "" {
		model = "default"
	}

	reqBody := chatRequest{
		Model:    model,
		Messages: messages,
		Tools:    r.registry.ListForOpenAI(),
	}

	systemMsg := ChatMessage{Role: "system", Content: r.agent.SystemPrompt}
	// Append AGENTS.md content if present (read fresh each time, not cached)
	if r.rootDir != "" {
		agentsMD, err := os.ReadFile(filepath.Join(r.rootDir, "AGENTS.md"))
		if err == nil && len(agentsMD) > 0 {
			systemMsg.Content += "\n\n" + string(agentsMD)
		}
	}
	fullMessages := append([]ChatMessage{systemMsg}, messages...)
	var stats TokenStats
	// Emit final token stats once when the turn completes
	defer func() {
		if stats.LLMCalls > 0 && r.onTokenStats != nil {
			r.onTokenStats(stats)
		}
	}()

	for {
		reqBody.Messages = fullMessages

		resp, err := r.callLLM(ctx, reqBody)
		if err != nil {
			return "", err
		}

		if resp.Error != nil {
			return "", fmt.Errorf("LLM error: %s", resp.Error.Message)
		}

		// Accumulate token stats
		if resp.Usage != nil {
			stats.PromptTokens += resp.Usage.PromptTokens
			stats.CompletionTokens += resp.Usage.CompletionTokens
			stats.TotalTokens += resp.Usage.TotalTokens
			stats.LLMCalls++
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("LLM returned no choices")
		}

		choice := resp.Choices[0]
		assistantMsg := choice.Message

		dlog.Log("LLM response content_len=%d tool_calls=%d finish=%s", len(assistantMsg.Content), len(assistantMsg.ToolCalls), choice.FinishReason)

		// Stream text
		if assistantMsg.Content != "" && r.onText != nil {
			r.onText(assistantMsg.Content)
		}

		// No tool calls → done
		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			// Execute on_turn_end hooks
			_ = r.executeHooks(ctx, agents.HookTurnEnd, userPrompt)
			return assistantMsg.Content, nil
		}

		// Process tool calls
		fullMessages = append(fullMessages, ChatMessage{
			Role:      "assistant",
			Content:   assistantMsg.Content,
			ToolCalls: assistantMsg.ToolCalls,
		})

		for _, tc := range assistantMsg.ToolCalls {
			if r.onToolStart != nil {
				r.onToolStart(tc.ID, tc.Function.Name, "other", tc.Function.Arguments)
			}
			if r.onToolUpdate != nil {
				r.onToolUpdate(tc.ID, "in_progress", "")
			}

			result, err := r.registry.CallTool(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
				if r.onToolUpdate != nil {
					r.onToolUpdate(tc.ID, "failed", result)
				}
			} else {
				if r.onToolUpdate != nil {
					r.onToolUpdate(tc.ID, "completed", result)
				}
			}

			// Ensure tool response always has content (some LLMs reject empty content)
			toolContent := result
			if toolContent == "" {
				toolContent = "(no output)"
			}
			fullMessages = append(fullMessages, ChatMessage{
				Role:       "tool",
				Content:    toolContent,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (r *Runner) callLLM(ctx context.Context, req chatRequest) (*chatResponse, error) {
	dlog := debuglog.For("runner")
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(r.llm.APIBase, "/") + "/chat/completions"
	dlog.Log("callLLM POST %s model=%s msg_count=%d", url, req.Model, len(req.Messages))
	dlog.ReqRes("REQUEST", string(body))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if r.llm.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.llm.APIKey)
	}
	for k, v := range r.llm.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read LLM response: %w", err)
	}

	dlog.Log("callLLM response HTTP %d len=%d", resp.StatusCode, len(respBody))
	dlog.ReqRes("RESPONSE", string(respBody))

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}
	return &chatResp, nil
}

// executeHooks runs all hooks for a given event, replacing %p with the user prompt.
func (r *Runner) executeHooks(ctx context.Context, hook string, userPrompt string) error {
	hooks := r.agent.HookTools(hook)
	for toolName, args := range hooks {
		resolved := make(map[string]any)
		for k, v := range args {
			resolved[k] = strings.ReplaceAll(v, "%p", userPrompt)
		}
		_, err := r.registry.CallTool(ctx, toolName, mustMarshal(resolved))
		if err != nil {
			return fmt.Errorf("hook %s/%s: %w", hook, toolName, err)
		}
	}
	return nil
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
