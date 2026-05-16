package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llmdevkit/internal/indexer"

	"github.com/mark3labs/mcp-go/mcp"
)

type indexerProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner

	mu sync.Mutex
}

var IdxProc *indexerProcess

type RetrieveResult struct {
	FilePath  string  `json:"file_path"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Language  string  `json:"language"`
	ChunkType string  `json:"chunk_type"`
	Signature string  `json:"signature"`
	Score     float64 `json:"score"`
}

func StartIndexer(rootDir string, ignore string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	llmdevkitBin := filepath.Join(filepath.Dir(exePath), "llmdevkit")
	if _, err := os.Stat(llmdevkitBin); os.IsNotExist(err) {
		llmdevkitBin, err = exec.LookPath("llmdevkit")
		if err != nil {
			return fmt.Errorf("llmdevkit not found")
		}
	}

	args := []string{"indexer", rootDir}
	if ignore != "" {
		args = []string{"indexer", fmt.Sprintf("--ignore=%s", ignore), rootDir}
	}
	if LlamaEmbedder != nil {
		args = []string{
			"indexer",
			fmt.Sprintf("--embedder-port=%d", LlamaEmbedder.Port()),
			rootDir,
		}
		if ignore != "" {
			args = []string{
				"indexer",
				fmt.Sprintf("--ignore=%s", ignore),
				fmt.Sprintf("--embedder-port=%d", LlamaEmbedder.Port()),
				rootDir,
			}
		}
		if LlamaReranker != nil {
			args = []string{
				"indexer",
				fmt.Sprintf("--embedder-port=%d", LlamaEmbedder.Port()),
				fmt.Sprintf("--reranker-port=%d", LlamaReranker.Port()),
				rootDir,
			}
			if ignore != "" {
				args = []string{
					"indexer",
					fmt.Sprintf("--ignore=%s", ignore),
					fmt.Sprintf("--embedder-port=%d", LlamaEmbedder.Port()),
					fmt.Sprintf("--reranker-port=%d", LlamaReranker.Port()),
					rootDir,
				}
			}
		}
	}

	cmd := exec.Command(llmdevkitBin, args...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)

	if !scanner.Scan() {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("indexer failed to start")
	}

	IdxProc = &indexerProcess{
		cmd:   cmd,
		stdin: stdin,
		out:   scanner,
	}

	fmt.Fprintf(os.Stderr, "[INFO] llmdevkit: indexer started (pid %d)\n", cmd.Process.Pid)
	return nil
}

func StopIndexer() {
	if IdxProc != nil {
		IdxProc.stdin.Close()
		done := make(chan error, 1)
		go func() {
			done <- IdxProc.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			IdxProc.cmd.Process.Kill()
		}
		IdxProc = nil
	}
}

func IndexerSend(cmd string) (string, error) {
	if IdxProc == nil {
		return "", fmt.Errorf("indexer not running")
	}

	IdxProc.mu.Lock()
	defer IdxProc.mu.Unlock()

	if _, err := fmt.Fprintln(IdxProc.stdin, cmd); err != nil {
		return "", err
	}

	if !IdxProc.out.Scan() {
		return "", fmt.Errorf("indexer closed")
	}

	return IdxProc.out.Text(), nil
}

func IndexerHealth() string {
	resp, err := IndexerSend("health")
	if err != nil {
		return ""
	}
	return resp
}

func RelevantCodeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	state := IndexerHealth()
	if state != "idle" {
		return mcp.NewToolResultText(""), nil
	}

	resp, err := IndexerSend("retrieve " + prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: indexer retrieve error: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	resp = strings.TrimSpace(resp)
	if resp == "" || strings.HasPrefix(resp, "error:") {
		return mcp.NewToolResultText(""), nil
	}

	var results []RetrieveResult
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: parse retrieve results: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	return mcp.NewToolResultText(FormatSignatureBlocks(results)), nil
}

func FormatSignatureBlocks(results []RetrieveResult) string {
	var blocks []string
	for _, r := range results {
		sig := r.Signature
		if sig == "" {
			sig = "-"
		}
		blocks = append(blocks, fmt.Sprintf("Signature: %s\nFile: %s\nLine Range: %d-%d\nLanguage: %s\nType: %s", sig, r.FilePath, r.LineStart, r.LineEnd, r.Language, r.ChunkType))
	}
	return strings.Join(blocks, "\n\n")
}

func SearchSymbolInCodeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, err := req.RequireString("symbol_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	state := IndexerHealth()
	if state != "idle" {
		return mcp.NewToolResultText(""), nil
	}

	resp, err := IndexerSend("search_signature " + symbolName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: indexer search_signature error: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	resp = strings.TrimSpace(resp)
	if resp == "" || strings.HasPrefix(resp, "error:") {
		return mcp.NewToolResultText(""), nil
	}

	var results []RetrieveResult
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: parse search_signature results: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	return mcp.NewToolResultText(FormatSignatureBlocks(results)), nil
}

// Llama types re-exported from internal/indexer
var (
	LlamaCtx       context.Context
	LlamaCancel    context.CancelFunc
	LlamaEmbedder  *indexer.LlamaServer
	LlamaReranker  *indexer.LlamaServer
	LlamaExtractor *indexer.LlamaServer
	LlamaMu        sync.Mutex
	LlamaReady     bool
)
