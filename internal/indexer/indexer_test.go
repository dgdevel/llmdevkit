package indexer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"llmdevkit/internal/cfg"
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

func newTestIndexer(t *testing.T) (*Indexer, string) {
	t.Helper()
	dir := t.TempDir()

	// Write config so Start() finds it
	cfgDir := cfg.DirPath(dir)
	os.MkdirAll(cfgDir, 0755)
	cfg.Write(map[string]map[string]string{
		"llama": {
			"search_count":    "50",
			"result_count":    "10",
			"reranker_enabled": "false",
		},
	}, cfg.FilePath(dir))

	idx := &Indexer{
		rootDir:  dir,
		ctx:      context.Background(),
		manifest: make(map[string]string),
	}

	// Create store with mock embedder
	store, err := NewStore(idx.ctx, filepath.Join(cfgDir, "index"), mockEmbedFn)
	if err != nil {
		t.Fatal(err)
	}
	idx.store = store

	return idx, dir
}

func TestScanAndIndexRemovesDeletedFiles(t *testing.T) {
	idx, dir := newTestIndexer(t)

	// Create a Go file
	goFile := filepath.Join(dir, "sample.go")
	os.WriteFile(goFile, []byte("package main\n\nfunc hello() {\n\tprintln(\"hi\")\n}\n"), 0644)

	// First scan
	fileCount, _ := idx.scanAndIndex()
	if fileCount < 1 {
		t.Fatalf("expected at least 1 file indexed, got %d", fileCount)
	}
	if _, ok := idx.manifest["sample.go"]; !ok {
		t.Fatal("sample.go should be in manifest after first scan")
	}
	if len(idx.signatures) == 0 {
		t.Fatal("expected signatures after first scan")
	}
	storeCount := idx.store.Count()
	if storeCount == 0 {
		t.Fatal("expected store to have entries after first scan")
	}

	// Delete the file
	os.Remove(goFile)

	// Second scan - should remove stale entries
	fileCount2, _ := idx.scanAndIndex()
	if fileCount2 != 0 {
		t.Errorf("expected 0 new files on second scan, got %d", fileCount2)
	}
	if _, ok := idx.manifest["sample.go"]; ok {
		t.Error("sample.go should be removed from manifest after deletion")
	}
	for _, s := range idx.signatures {
		if s.FilePath == "sample.go" {
			t.Error("sample.go signatures should have been removed")
		}
	}
	if idx.store.Count() != 0 {
		t.Errorf("expected store to be empty after file removal, got %d entries", idx.store.Count())
	}
}

func TestRemoveFileSignatures(t *testing.T) {
	entries := []SignatureEntry{
		{FilePath: "a.go", Signature: "func A()"},
		{FilePath: "b.go", Signature: "func B()"},
		{FilePath: "a.go", Signature: "func C()"},
	}

	filtered := removeFileSignatures(entries, "a.go")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(filtered))
	}
	if filtered[0].FilePath != "b.go" {
		t.Errorf("expected b.go, got %s", filtered[0].FilePath)
	}
}
