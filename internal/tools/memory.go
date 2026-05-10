package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"llmdevkit/internal/indexer"
	"llmdevkit/internal/memory"

	"github.com/mark3labs/mcp-go/mcp"
)

var (
	MemStore *memory.Memory
	MemMu    sync.Mutex
)

const MemoryExtractSystemPrompt = `You are a fact extraction assistant. Extract discrete, self-contained factual statements from the text below. Rules:
- One fact per line.
- Each fact must be a complete, standalone sentence.
- Skip opinions, questions, and vague statements.
- Skip facts that are already common knowledge (e.g. "the sky is blue").
- Preserve specific details: names, dates, quantities, locations, decisions, preferences.
- If no extractable facts are found, output nothing.`

const defaultExtractorModel = "unsloth/Qwen3.5-0.8B-GGUF"
const defaultExtractorQuant = "UD-Q4_K_XL"

func StartMemory(rootDir string) error {
	if !LlamaReady {
		return fmt.Errorf("llama servers not started (missing [llama] config?)")
	}
	if LlamaEmbedder == nil {
		return fmt.Errorf("embedder server not available")
	}

	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return LlamaEmbedder.GetEmbeddingOpenAI(ctx, text)
	}

	memDir := memory.DirPath(rootDir)
	store, err := memory.NewMemory(LlamaCtx, memDir, embedFn)
	if err != nil {
		return fmt.Errorf("initializing memory: %w", err)
	}
	MemStore = store

	fmt.Fprintf(os.Stderr, "[INFO] Memory store ready (%d facts)\n", MemStore.Count())
	return nil
}

func StopMemory() {
	MemStore = nil
}

func MemoryPutHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fact, err := req.RequireString("fact")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	MemMu.Lock()
	defer MemMu.Unlock()

	if MemStore == nil {
		return mcp.NewToolResultError("memory not initialized"), nil
	}

	if err := MemStore.Put(ctx, fact); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store fact: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Fact memorized (%d total facts)", MemStore.Count())), nil
}

func RelevantMemoryHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	MemMu.Lock()
	defer MemMu.Unlock()

	if MemStore == nil {
		return mcp.NewToolResultError("memory not initialized"), nil
	}

	facts, err := MemStore.Retrieve(ctx, prompt, 10)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to retrieve: %v", err)), nil
	}

	if len(facts) == 0 {
		return mcp.NewToolResultText("No relevant memories found."), nil
	}

	data, _ := json.MarshalIndent(facts, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func MemoryExtractHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	MemMu.Lock()
	extractor := LlamaExtractor
	store := MemStore
	MemMu.Unlock()

	if extractor == nil {
		return mcp.NewToolResultError("memory extractor not initialized (no chat model configured)"), nil
	}
	if store == nil {
		return mcp.NewToolResultError("memory not initialized"), nil
	}

	// Call the small LLM to extract facts
	messages := []indexer.ChatMessage{
		{Role: "system", Content: MemoryExtractSystemPrompt},
		{Role: "user", Content: text},
	}

	response, err := extractor.ChatCompletion(ctx, messages)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("extraction failed: %v", err)), nil
	}

	// Parse response into individual facts
	lines := strings.Split(response, "\n")
	var extracted []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines, bullet prefixes, numbered prefixes
		line = strings.TrimLeft(line, "-•*0123456789. )")
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 10 {
			continue
		}
		// Skip common non-fact patterns
		if strings.HasPrefix(strings.ToLower(line), "here are") ||
			strings.HasPrefix(strings.ToLower(line), "no facts") ||
			strings.HasPrefix(strings.ToLower(line), "no extract") ||
			strings.HasPrefix(strings.ToLower(line), "the text") ||
			strings.HasPrefix(strings.ToLower(line), "note:") {
			continue
		}
		extracted = append(extracted, line)
	}

	if len(extracted) == 0 {
		return mcp.NewToolResultText("No extractable facts found in the text."), nil
	}

	// For each extracted fact, check vector DB for similar facts and dedup/refine
	type factResult struct {
		Fact    string `json:"fact"`
		Action  string `json:"action"`
		Similar string `json:"similar,omitempty"`
	}

	var results []factResult
	for _, fact := range extracted {
		// Check for similar existing facts
		similar, _ := store.QuerySimilar(ctx, fact, 3, 0.85)

		if len(similar) > 0 {
			// Check if the extracted fact is essentially the same as an existing one
			bestMatch := similar[0]

			if bestMatch.Score > 0.93 {
				// Nearly identical — skip, don't store duplicate
				results = append(results, factResult{
					Fact:    fact,
					Action:  "skipped_duplicate",
					Similar: bestMatch.Fact,
				})
				continue
			}

			// Similar but not identical — this may be a refinement.
			// Use the extractor to decide if the new fact adds info.
			refineMsgs := []indexer.ChatMessage{
				{Role: "system", Content: "You are comparing two factual statements. Does statement B add new information not present in statement A, or is it just a rephrasing? Answer ONLY 'new' or 'rephrase'."},
				{Role: "user", Content: fmt.Sprintf("Statement A: %s\nStatement B: %s", bestMatch.Fact, fact)},
			}
			verdict, err := extractor.ChatCompletion(ctx, refineMsgs)
			if err == nil {
				verdict = strings.TrimSpace(strings.ToLower(verdict))
				if strings.Contains(verdict, "rephrase") {
					results = append(results, factResult{
						Fact:    fact,
						Action:  "skipped_rephrase",
						Similar: bestMatch.Fact,
					})
					continue
				}
			}
		}

		// Store the new fact
		if err := store.Put(ctx, fact); err != nil {
			results = append(results, factResult{
				Fact:   fact,
				Action: fmt.Sprintf("error: %v", err),
			})
			continue
		}

		action := "stored"
		if len(similar) > 0 {
			action = "stored_refined"
		}
		results = append(results, factResult{
			Fact:   fact,
			Action: action,
		})
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}
