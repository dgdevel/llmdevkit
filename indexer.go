package main

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

	"github.com/mark3labs/mcp-go/mcp"
)

type indexerProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner

	mu sync.Mutex
}

var idxProc *indexerProcess

type retrieveResult struct {
	FilePath  string  `json:"file_path"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Language  string  `json:"language"`
	ChunkType string  `json:"chunk_type"`
	Signature string  `json:"signature"`
	Score     float64 `json:"score"`
}

func startIndexer(rootDir string, ignore string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	indexerBin := filepath.Join(filepath.Dir(exePath), "llmdevkit-indexer")
	if _, err := os.Stat(indexerBin); os.IsNotExist(err) {
		indexerBin, err = exec.LookPath("llmdevkit-indexer")
		if err != nil {
			return fmt.Errorf("llmdevkit-indexer not found")
		}
	}

	args := []string{rootDir}
	if ignore != "" {
		args = append([]string{fmt.Sprintf("--ignore=%s", ignore)}, args...)
	}
	if llamaEmbedder != nil {
		args = []string{
			fmt.Sprintf("--embedder-port=%d", llamaEmbedder.Port()),
			rootDir,
		}
		if ignore != "" {
			args = append([]string{fmt.Sprintf("--ignore=%s", ignore)}, args...)
		}
		if llamaReranker != nil {
			args = []string{
				fmt.Sprintf("--embedder-port=%d", llamaEmbedder.Port()),
				fmt.Sprintf("--reranker-port=%d", llamaReranker.Port()),
				rootDir,
			}
			if ignore != "" {
				args = append([]string{fmt.Sprintf("--ignore=%s", ignore)}, args...)
			}
		}
	}

	cmd := exec.Command(indexerBin, args...)
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

	idxProc = &indexerProcess{
		cmd:   cmd,
		stdin: stdin,
		out:   scanner,
	}

	fmt.Fprintf(os.Stderr, "[INFO] llmdevkit: indexer started (pid %d)\n", cmd.Process.Pid)
	return nil
}

func stopIndexer() {
	if idxProc != nil {
		idxProc.stdin.Close()
		done := make(chan error, 1)
		go func() {
			done <- idxProc.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			idxProc.cmd.Process.Kill()
		}
		idxProc = nil
	}
}

func indexerSend(cmd string) (string, error) {
	if idxProc == nil {
		return "", fmt.Errorf("indexer not running")
	}

	idxProc.mu.Lock()
	defer idxProc.mu.Unlock()

	if _, err := fmt.Fprintln(idxProc.stdin, cmd); err != nil {
		return "", err
	}

	if !idxProc.out.Scan() {
		return "", fmt.Errorf("indexer closed")
	}

	return idxProc.out.Text(), nil
}

func indexerHealth() string {
	resp, err := indexerSend("health")
	if err != nil {
		return ""
	}
	return resp
}

func relevantCodeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	state := indexerHealth()
	if state != "idle" {
		return mcp.NewToolResultText(""), nil
	}

	resp, err := indexerSend("retrieve " + prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: indexer retrieve error: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	resp = strings.TrimSpace(resp)
	if resp == "" || strings.HasPrefix(resp, "error:") {
		return mcp.NewToolResultText(""), nil
	}

	var results []retrieveResult
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: parse retrieve results: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	return mcp.NewToolResultText(formatSignatureBlocks(results)), nil
}

func formatSignatureBlocks(results []retrieveResult) string {
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

func searchSymbolInCodeHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbolName, err := req.RequireString("symbol_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	state := indexerHealth()
	if state != "idle" {
		return mcp.NewToolResultText(""), nil
	}

	resp, err := indexerSend("search_signature " + symbolName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: indexer search_signature error: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	resp = strings.TrimSpace(resp)
	if resp == "" || strings.HasPrefix(resp, "error:") {
		return mcp.NewToolResultText(""), nil
	}

	var results []retrieveResult
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] llmdevkit: parse search_signature results: %v\n", err)
		return mcp.NewToolResultText(""), nil
	}

	return mcp.NewToolResultText(formatSignatureBlocks(results)), nil
}
