package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/philippgille/chromem-go"
)

type MemoryFact struct {
	ID          string  `json:"id"`
	Fact        string  `json:"fact"`
	CreatedAt   string  `json:"created_at"`
	LastUsed    string  `json:"last_used"`
	RecallCount int     `json:"recall_count"`
	Score       float64 `json:"score"`
}

type Memory struct {
	db         *chromem.DB
	collection *chromem.Collection
	embedFunc  chromem.EmbeddingFunc
	mu         sync.Mutex
}

func factID(fact string) string {
	h := sha256.Sum256([]byte(fact))
	return fmt.Sprintf("%x", h[:16])
}

func NewMemory(ctx context.Context, memDir string, embedFn func(ctx context.Context, text string) ([]float32, error)) (*Memory, error) {
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return nil, fmt.Errorf("creating memory dir: %w", err)
	}

	db, err := chromem.NewPersistentDB(memDir, true)
	if err != nil {
		return nil, fmt.Errorf("opening memory DB: %w", err)
	}

	embedFunc := chromem.EmbeddingFunc(embedFn)
	collection, err := db.GetOrCreateCollection("memory", nil, embedFunc)
	if err != nil {
		return nil, fmt.Errorf("creating memory collection: %w", err)
	}

	return &Memory{
		db:         db,
		collection: collection,
		embedFunc:  embedFunc,
	}, nil
}

func (m *Memory) Put(ctx context.Context, fact string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := factID(fact)
	now := time.Now().UTC().Format(time.RFC3339)

	// Check if exists - update created_at only on first insert
	// chromem AddDocuments with same ID overwrites
	doc := chromem.Document{
		ID:      id,
		Content: fact,
		Metadata: map[string]string{
			"fact":         fact,
			"created_at":   now,
			"last_used":    now,
			"recall_count": "0",
		},
	}

	return m.collection.AddDocuments(ctx, []chromem.Document{doc}, runtime.NumCPU())
}

func (m *Memory) Retrieve(ctx context.Context, prompt string, topN int) ([]MemoryFact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := m.collection.Count()
	if count == 0 {
		return nil, nil
	}
	if topN > count {
		topN = count
	}

	emb, err := m.embedFunc(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("embedding prompt: %w", err)
	}

	results, err := m.collection.QueryEmbedding(ctx, emb, topN, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("querying memory: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	facts := make([]MemoryFact, 0, len(results))
	for _, r := range results {
		recalls, _ := strconv.Atoi(r.Metadata["recall_count"])
		recalls++

		// Update metadata by re-adding the document
		doc := chromem.Document{
			ID:      r.ID,
			Content: r.Metadata["fact"],
			Metadata: map[string]string{
				"fact":         r.Metadata["fact"],
				"created_at":   r.Metadata["created_at"],
				"last_used":    now,
				"recall_count": strconv.Itoa(recalls),
			},
		}
		if err := m.collection.AddDocuments(ctx, []chromem.Document{doc}, runtime.NumCPU()); err != nil {
			// Log but don't fail the retrieval
			fmt.Fprintf(os.Stderr, "[WARN] memory: failed to update recall for %s: %v\n", r.ID, err)
		}

		facts = append(facts, MemoryFact{
			ID:          r.ID,
			Fact:        r.Metadata["fact"],
			CreatedAt:   r.Metadata["created_at"],
			LastUsed:    now,
			RecallCount: recalls,
			Score:       float64(r.Similarity),
		})
	}

	return facts, nil
}

// QuerySimilar finds similar facts without updating recall counters.
// Returns facts with similarity scores above the given threshold.
func (m *Memory) QuerySimilar(ctx context.Context, text string, topN int, threshold float64) ([]MemoryFact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := m.collection.Count()
	if count == 0 {
		return nil, nil
	}
	if topN > count {
		topN = count
	}

	emb, err := m.embedFunc(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("embedding text: %w", err)
	}

	results, err := m.collection.QueryEmbedding(ctx, emb, topN, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("querying memory: %w", err)
	}

	facts := make([]MemoryFact, 0, len(results))
	for _, r := range results {
		score := float64(r.Similarity)
		if score < threshold {
			continue
		}
		recalls, _ := strconv.Atoi(r.Metadata["recall_count"])
		facts = append(facts, MemoryFact{
			ID:          r.ID,
			Fact:        r.Metadata["fact"],
			CreatedAt:   r.Metadata["created_at"],
			LastUsed:    r.Metadata["last_used"],
			RecallCount: recalls,
			Score:       score,
		})
	}

	return facts, nil
}

// UpdateFact replaces an existing fact with a refined version.
// The old ID is removed and a new one is created.
func (m *Memory) UpdateFact(ctx context.Context, oldID, newFact string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete old document
	if err := m.collection.Delete(ctx, nil, nil, oldID); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] memory: failed to delete old fact %s: %v\n", oldID, err)
	}

	// Add new fact
	now := time.Now().UTC().Format(time.RFC3339)
	doc := chromem.Document{
		ID:      factID(newFact),
		Content: newFact,
		Metadata: map[string]string{
			"fact":         newFact,
			"created_at":   now,
			"last_used":    now,
			"recall_count": "0",
		},
	}

	return m.collection.AddDocuments(ctx, []chromem.Document{doc}, runtime.NumCPU())
}

func (m *Memory) Count() int {
	return m.collection.Count()
}

// DirPath returns the memory directory under .llmdevkit
func DirPath(rootDir string) string {
	return filepath.Join(rootDir, ".llmdevkit", "memory")
}
