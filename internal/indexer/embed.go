package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type LlamaServer struct {
	cmd     *exec.Cmd
	port    int
	baseURL string
}

// NewLlamaServerOnPort creates a LlamaServer client pointing at an already-running server.
func NewLlamaServerOnPort(port int) *LlamaServer {
	return &LlamaServer{
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatChoice struct {
	Message ChatMessage `json:"message"`
}

type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
}

func (s *LlamaServer) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":    "default",
		"messages": messages,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result ChatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing chat response: %w (body: %s)", err, string(respBody))
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty chat response (body: %s)", string(respBody))
	}

	return result.Choices[0].Message.Content, nil
}

func StartServer(ctx context.Context, exeWithArgs, hfModel string, extraArgs ...string) (*LlamaServer, error) {
	port, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("getting free port: %w", err)
	}

	parts := strings.Fields(exeWithArgs)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty exe path")
	}

	// Detect which default flags are overridden by extraArgs so we don't duplicate them.
	// llama.cpp takes the first occurrence, so appending after defaults would lose the override.
	overrideSet := map[string]bool{}
	for _, a := range extraArgs {
		overrideSet[a] = true
	}
	defaultFlags := []string{"--ctx-size", "2048", "--batch-size", "2048", "--ubatch-size", "2048", "-np", "1"}
	var filteredDefaults []string
	for i := 0; i < len(defaultFlags); i += 2 {
		flag := defaultFlags[i]
		if !overrideSet[flag] {
			filteredDefaults = append(filteredDefaults, flag, defaultFlags[i+1])
		}
	}

	args := append(parts[1:], "-hf", hfModel, "--port", fmt.Sprintf("%d", port), "--host", "127.0.0.1")
	args = append(args, filteredDefaults...)
	args = append(args, extraArgs...)

	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	fmt.Fprintf(os.Stderr, "[INFO] Starting: %s %s\n", parts[0], strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting llama-server: %w", err)
	}

	srv := &LlamaServer{
		cmd:     cmd,
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
	}

	if err := srv.waitForReady(ctx, 120*time.Second); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}

	return srv, nil
}

func (s *LlamaServer) Port() int { return s.port }

func (s *LlamaServer) waitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, _ := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v", timeout)
}

func (s *LlamaServer) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"content": text})
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/embedding", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result []struct {
		Embedding interface{} `json:"embedding"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing embedding response: %w (body: %s)", err, string(respBody))
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}

	return toFloat32Slice(result[0].Embedding)
}

func (s *LlamaServer) GetEmbeddingOpenAI(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"input": text,
		"model": "default",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []struct {
			Embedding interface{} `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing embedding response: %w (body: %s)", err, string(respBody))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response (body: %s)", string(respBody))
	}

	return toFloat32Slice(result.Data[0].Embedding)
}

type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (s *LlamaServer) Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"query":     query,
		"documents": documents,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Results []RerankResult `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing rerank response: %w (body: %s)", err, string(respBody))
	}

	return result.Results, nil
}

func (s *LlamaServer) Stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() {
			done <- s.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			s.cmd.Process.Kill()
		}
	}
}

func toFloat32Slice(v interface{}) ([]float32, error) {
	switch arr := v.(type) {
	case []interface{}:
		result := make([]float32, len(arr))
		for i, val := range arr {
			switch f := val.(type) {
			case float64:
				result[i] = float32(f)
			case json.Number:
				f64, _ := f.Float64()
				result[i] = float32(f64)
			default:
				return nil, fmt.Errorf("unexpected type in embedding: %T", val)
			}
		}
		return result, nil
	case []float64:
		result := make([]float32, len(arr))
		for i, f := range arr {
			result[i] = float32(f)
		}
		return result, nil
	case []float32:
		return arr, nil
	default:
		return nil, fmt.Errorf("unexpected embedding type: %T", v)
	}
}
