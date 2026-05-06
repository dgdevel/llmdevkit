package memory

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func mockEmbedFn(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, 32)
	for i := range text {
		vec[i%32] += float32(text[i])
	}
	norm := float32(0)
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec, nil
}

func newTestMemory(t *testing.T) *Memory {
	t.Helper()
	dir := t.TempDir()
	m, err := NewMemory(context.Background(), dir, mockEmbedFn)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewMemory(t *testing.T) {
	m := newTestMemory(t)
	if m == nil {
		t.Fatal("expected non-nil Memory")
	}
	if m.Count() != 0 {
		t.Errorf("expected 0 facts, got %d", m.Count())
	}
}

func TestNewMemoryCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "memory")
	_, err := NewMemory(context.Background(), dir, mockEmbedFn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected memory directory to be created")
	}
}

func TestPut(t *testing.T) {
	m := newTestMemory(t)

	err := m.Put(context.Background(), "the sky is blue")
	if err != nil {
		t.Fatal(err)
	}
	if m.Count() != 1 {
		t.Errorf("expected 1 fact, got %d", m.Count())
	}
}

func TestPutMultiple(t *testing.T) {
	m := newTestMemory(t)

	facts := []string{
		"the sky is blue",
		"water is wet",
		"fire is hot",
	}
	for _, f := range facts {
		if err := m.Put(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	if m.Count() != 3 {
		t.Errorf("expected 3 facts, got %d", m.Count())
	}
}

func TestPutDeduplication(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}
	if m.Count() != 1 {
		t.Errorf("expected 1 fact (deduplicated), got %d", m.Count())
	}
}

func TestRetrieveBasic(t *testing.T) {
	m := newTestMemory(t)

	facts := []string{
		"the sky is blue",
		"water is wet",
		"fire is hot",
		"grass is green",
		"ice is cold",
	}
	for _, f := range facts {
		if err := m.Put(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}

	results, err := m.Retrieve(context.Background(), "what color is the sky", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}

	found := false
	for _, r := range results {
		if r.Fact == "the sky is blue" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'the sky is blue' in results, got %v", results)
	}
}

func TestRetrieveUpdatesRecallCount(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}

	results, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].RecallCount != 1 {
		t.Errorf("expected recall_count=1 after first retrieval, got %d", results[0].RecallCount)
	}

	results2, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if results2[0].RecallCount != 2 {
		t.Errorf("expected recall_count=2 after second retrieval, got %d", results2[0].RecallCount)
	}
}

func TestRetrieveUpdatesLastUsed(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}

	results, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	firstLastUsed := results[0].LastUsed
	if firstLastUsed == "" {
		t.Error("expected non-empty last_used")
	}

	results2, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if results2[0].LastUsed < firstLastUsed {
		t.Errorf("last_used should be >= first last_used")
	}
}

func TestRetrievePreservesCreatedAt(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}

	results, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	origCreatedAt := results[0].CreatedAt

	results2, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if results2[0].CreatedAt != origCreatedAt {
		t.Errorf("created_at should not change after retrieval, got %q want %q", results2[0].CreatedAt, origCreatedAt)
	}
}

func TestRetrieveEmpty(t *testing.T) {
	m := newTestMemory(t)

	results, err := m.Retrieve(context.Background(), "anything", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty store, got %d", len(results))
	}
}

func TestRetrieveTopN(t *testing.T) {
	m := newTestMemory(t)

	for i := 0; i < 20; i++ {
		fact := string(rune('a'+i%26)) + " fact number"
		if err := m.Put(context.Background(), fact); err != nil {
			t.Fatal(err)
		}
	}

	results, err := m.Retrieve(context.Background(), "fact", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

func TestRetrieveHasScore(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}

	results, err := m.Retrieve(context.Background(), "sky color", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Score == 0 {
		t.Error("expected non-zero score")
	}
}

func TestFactIDDeterministic(t *testing.T) {
	id1 := factID("hello world")
	id2 := factID("hello world")
	if id1 != id2 {
		t.Errorf("factID should be deterministic, got %q and %q", id1, id2)
	}

	id3 := factID("hello world!")
	if id1 == id3 {
		t.Error("factID should differ for different inputs")
	}
}

func TestDirPath(t *testing.T) {
	got := DirPath("/tmp/project")
	want := "/tmp/project/.llmdevkit/memory"
	if got != want {
		t.Errorf("DirPath = %q, want %q", got, want)
	}
}

func TestMemoryFactHasID(t *testing.T) {
	m := newTestMemory(t)

	if err := m.Put(context.Background(), "the sky is blue"); err != nil {
		t.Fatal(err)
	}

	results, err := m.Retrieve(context.Background(), "sky", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].ID == "" {
		t.Error("expected non-empty ID")
	}
	expectedID := factID("the sky is blue")
	if results[0].ID != expectedID {
		t.Errorf("ID = %q, want %q", results[0].ID, expectedID)
	}
}
