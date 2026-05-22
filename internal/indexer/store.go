package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/philippgille/chromem-go"
)

type Store struct {
	db         *chromem.DB
	collection *chromem.Collection
	indexDir   string
	embedFunc  chromem.EmbeddingFunc
}

type SearchResult struct {
	FilePath   string
	LineStart  int
	LineEnd    int
	Signature  string
	Language   string
	ChunkType  string
	Content    string
	Similarity float64
}

func NewStore(ctx context.Context, indexDir string, embedFn func(ctx context.Context, text string) ([]float32, error)) (*Store, error) {
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		return nil, fmt.Errorf("creating index dir: %w", err)
	}

	db, err := chromem.NewPersistentDB(indexDir, true)
	if err != nil {
		return nil, fmt.Errorf("opening persistent DB: %w", err)
	}

	embedFunc := chromem.EmbeddingFunc(embedFn)
	collection, err := db.GetOrCreateCollection("code", nil, embedFunc)
	if err != nil {
		return nil, fmt.Errorf("creating collection: %w", err)
	}

	return &Store{
		db:         db,
		collection: collection,
		indexDir:   indexDir,
		embedFunc:  embedFunc,
	}, nil
}

func (s *Store) AddChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	docs := make([]chromem.Document, len(chunks))
	for i, c := range chunks {
		docs[i] = chromem.Document{
			ID:      FmtChunkID(c.FilePath, c.LineStart, c.LineEnd),
			Content: c.Content,
			Metadata: map[string]string{
				"source":     "llmdevkit indexer",
				"file_path":  c.FilePath,
				"line_start": strconv.Itoa(c.LineStart),
				"line_end":   strconv.Itoa(c.LineEnd),
				"signature":  c.Signature,
				"language":   c.Language,
				"chunk_type": c.ChunkType,
			},
		}
	}

	return s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

func (s *Store) RemoveFile(ctx context.Context, filePath string) error {
	return s.collection.Delete(ctx, map[string]string{
		"file_path": filePath,
	}, nil)
}

func (s *Store) Search(ctx context.Context, embedding []float32, topN int) ([]SearchResult, error) {
	results, err := s.collection.QueryEmbedding(ctx, embedding, topN, nil, nil)
	if err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		ls, _ := strconv.Atoi(r.Metadata["line_start"])
		le, _ := strconv.Atoi(r.Metadata["line_end"])
		out = append(out, SearchResult{
			FilePath:   r.Metadata["file_path"],
			LineStart:  ls,
			LineEnd:    le,
			Signature:  r.Metadata["signature"],
			Language:   r.Metadata["language"],
			ChunkType:  r.Metadata["chunk_type"],
			Content:    r.Content,
			Similarity: float64(r.Similarity),
		})
	}
	return out, nil
}

func (s *Store) Reset(ctx context.Context) error {
	path := filepath.Join(s.indexDir, "code.gob.gz")
	os.Remove(path)

	collection, err := s.db.GetOrCreateCollection("code", nil, s.embedFunc)
	if err != nil {
		return err
	}
	s.collection = collection
	return nil
}

func (s *Store) Count() int {
	return s.collection.Count()
}
