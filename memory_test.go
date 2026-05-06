package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"llmdevkit/internal/memory"

	"github.com/mark3labs/mcp-go/mcp"
)

func setupTestMemory(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	rootDir = root

	memDir := memory.DirPath(rootDir)
	ctx := context.Background()
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		vec := make([]float32, 32)
		for i := range text {
			vec[i%32] += float32(text[i])
		}
		return vec, nil
	}

	store, err := memory.NewMemory(ctx, memDir, embedFn)
	if err != nil {
		t.Fatal(err)
	}

	memStore = store
	memMu = sync.Mutex{}
}

func memPutReq(fact string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "memory_put",
			Arguments: map[string]interface{}{
				"fact": fact,
			},
		},
	}
}

func relMemReq(prompt string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "relevant_memory",
			Arguments: map[string]interface{}{
				"prompt": prompt,
			},
		},
	}
}

func TestMemoryPutHandler(t *testing.T) {
	setupTestMemory(t)

	result, err := memoryPutHandler(context.Background(), memPutReq("the sky is blue"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", textOf(t, result))
	}

	text := textOf(t, result)
	if !strings.Contains(text, "Fact memorized") {
		t.Errorf("expected 'Fact memorized' in response, got %q", text)
	}
	if !strings.Contains(text, "1 total facts") {
		t.Errorf("expected '1 total facts' in response, got %q", text)
	}
}

func TestMemoryPutHandlerMultiple(t *testing.T) {
	setupTestMemory(t)

	facts := []string{"the sky is blue", "water is wet", "fire is hot"}
	for i, f := range facts {
		result, err := memoryPutHandler(context.Background(), memPutReq(f))
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("unexpected error on fact %d: %s", i, textOf(t, result))
		}
		text := textOf(t, result)
		want := "3 total facts"
		if i < 2 {
			want = ""
		}
		if i == 2 && !strings.Contains(text, want) {
			t.Errorf("expected %q in response, got %q", want, text)
		}
	}
}

func TestMemoryPutHandlerMissingFact(t *testing.T) {
	setupTestMemory(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "memory_put",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := memoryPutHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing fact parameter")
	}
}

func TestMemoryPutHandlerNilStore(t *testing.T) {
	memStore = nil

	result, err := memoryPutHandler(context.Background(), memPutReq("test"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when memory not initialized")
	}
}

func TestRelevantMemoryHandler(t *testing.T) {
	setupTestMemory(t)

	facts := []string{"the sky is blue", "water is wet", "fire is hot"}
	for _, f := range facts {
		memoryPutHandler(context.Background(), memPutReq(f))
	}

	result, err := relevantMemoryHandler(context.Background(), relMemReq("what color is the sky"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", textOf(t, result))
	}

	text := textOf(t, result)
	if text == "No relevant memories found." {
		t.Fatal("expected to find relevant memories")
	}

	var memFacts []memory.MemoryFact
	if err := json.Unmarshal([]byte(text), &memFacts); err != nil {
		t.Fatalf("failed to parse result as JSON: %v\n%s", err, text)
	}

	if len(memFacts) == 0 {
		t.Fatal("expected at least one fact")
	}
}

func TestRelevantMemoryHandlerEmpty(t *testing.T) {
	setupTestMemory(t)

	result, err := relevantMemoryHandler(context.Background(), relMemReq("anything"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", textOf(t, result))
	}

	text := textOf(t, result)
	if text != "No relevant memories found." {
		t.Errorf("expected no memories message, got %q", text)
	}
}

func TestRelevantMemoryHandlerMissingPrompt(t *testing.T) {
	setupTestMemory(t)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "relevant_memory",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := relevantMemoryHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing prompt parameter")
	}
}

func TestRelevantMemoryHandlerNilStore(t *testing.T) {
	memStore = nil

	result, err := relevantMemoryHandler(context.Background(), relMemReq("test"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when memory not initialized")
	}
}

func TestRelevantMemoryRecallCounter(t *testing.T) {
	setupTestMemory(t)

	memoryPutHandler(context.Background(), memPutReq("the sky is blue"))

	result1, err := relevantMemoryHandler(context.Background(), relMemReq("sky"))
	if err != nil {
		t.Fatal(err)
	}
	if result1.IsError {
		t.Fatalf("unexpected error: %s", textOf(t, result1))
	}

	var facts1 []memory.MemoryFact
	json.Unmarshal([]byte(textOf(t, result1)), &facts1)
	if len(facts1) == 0 {
		t.Fatal("expected at least one fact")
	}
	if facts1[0].RecallCount != 1 {
		t.Errorf("expected recall_count=1, got %d", facts1[0].RecallCount)
	}

	result2, err := relevantMemoryHandler(context.Background(), relMemReq("sky"))
	if err != nil {
		t.Fatal(err)
	}
	var facts2 []memory.MemoryFact
	json.Unmarshal([]byte(textOf(t, result2)), &facts2)
	if facts2[0].RecallCount != 2 {
		t.Errorf("expected recall_count=2, got %d", facts2[0].RecallCount)
	}
}
