package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llmdevkit/internal/cfg"

	"github.com/fsnotify/fsnotify"
)

type State string

const (
	StateStarting State = "starting"
	StateIndexing State = "indexing"
	StateIdle     State = "idle"
)

type RetrieveResult struct {
	Source    string  `json:"source"`
	FilePath  string  `json:"file_path"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Signature string  `json:"signature"`
	Language  string  `json:"language"`
	ChunkType string  `json:"chunk_type"`
	Score     float64 `json:"score"`
	Content   string  `json:"content"`
}

type Indexer struct {
	rootDir  string
	config   map[string]map[string]string
	state    State
	stateMu  sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc

	embedder *LlamaServer
	reranker *LlamaServer
	store    *Store
	watcher  *fsnotify.Watcher

	searchCount     int
	resultCount     int
	rerankerEnabled bool
	externalServers bool // if true, servers were provided externally and won't be stopped

	manifest      map[string]string
	manifestMu    sync.Mutex

	signatures []SignatureEntry

	ignoreGlobs []string
}

type SignatureEntry struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Language  string `json:"language"`
	ChunkType string `json:"chunk_type"`
	Signature string `json:"signature"`
}

func NewIndexer(rootDir string, ignoreGlobs []string) *Indexer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Indexer{
		rootDir:     rootDir,
		ctx:         ctx,
		cancel:      cancel,
		state:       StateStarting,
		manifest:    make(map[string]string),
		ignoreGlobs: ignoreGlobs,
	}
}

// NewIndexerWithServers creates an Indexer that uses pre-started llama servers
// instead of starting its own.
func NewIndexerWithServers(rootDir string, embedder, reranker *LlamaServer, ignoreGlobs []string) *Indexer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Indexer{
		rootDir:         rootDir,
		ctx:             ctx,
		cancel:          cancel,
		state:           StateStarting,
		manifest:        make(map[string]string),
		embedder:        embedder,
		reranker:        reranker,
		rerankerEnabled: reranker != nil,
		externalServers: true,
		ignoreGlobs:     ignoreGlobs,
	}
}

func (idx *Indexer) shouldIgnore(path string) bool {
	if len(idx.ignoreGlobs) == 0 {
		return false
	}
	rel, err := filepath.Rel(idx.rootDir, path)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		for _, g := range idx.ignoreGlobs {
			if ok, _ := filepath.Match(g, part); ok {
				return true
			}
		}
	}
	return false
}

func (idx *Indexer) Start() error {
	config := cfg.MergedRead(idx.rootDir)
	idx.config = config

	llamaCfg := config["llama"]
	if llamaCfg == nil {
		return fmt.Errorf("missing [llama] config section")
	}

	idx.searchCount = cfg.Atoi(llamaCfg["search_count"], 50)
	idx.resultCount = cfg.Atoi(llamaCfg["result_count"], 10)

	var err error

	// If embedder not pre-set, start servers ourselves
	if idx.embedder == nil {
		llamaPath := llamaCfg["path"]
		if llamaPath == "" {
			return fmt.Errorf("missing llama.path config")
		}

		embedderRepo := llamaCfg["embedder"]
		if embedderRepo == "" {
			return fmt.Errorf("missing llama.embedder config")
		}

		idx.rerankerEnabled = !cfg.IsDisabled(llamaCfg["reranker_enabled"])
		if idx.rerankerEnabled {
			rerankerRepo := llamaCfg["reranker"]
			if rerankerRepo == "" {
				return fmt.Errorf("missing llama.reranker config")
			}
		}

		embedderFlags := strings.Fields(llamaCfg["embedder_flags"])

		t := time.Now()
		fmt.Fprintf(os.Stderr, "[INFO] Starting embedder server...\n")
		idx.embedder, err = StartServer(idx.ctx, llamaPath, embedderRepo, append(embedderFlags, "--embedding")...)
		if err != nil {
			return fmt.Errorf("starting embedder: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[INFO] Embedder on port %d (started in %s)\n", idx.embedder.port, time.Since(t).Round(time.Millisecond))

		if idx.rerankerEnabled {
			rerankerRepo := llamaCfg["reranker"]
			t = time.Now()
			fmt.Fprintf(os.Stderr, "[INFO] Starting reranker server...\n")
			idx.reranker, err = StartServer(idx.ctx, llamaPath, rerankerRepo, "--reranking")
			if err != nil {
				return fmt.Errorf("starting reranker: %w", err)
			}
			fmt.Fprintf(os.Stderr, "[INFO] Reranker on port %d (started in %s)\n", idx.reranker.port, time.Since(t).Round(time.Millisecond))
		} else {
			fmt.Fprintf(os.Stderr, "[INFO] Reranker disabled\n")
		}
	}

	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return idx.embedder.GetEmbeddingOpenAI(ctx, text)
	}

	indexDir := filepath.Join(cfg.DirPath(idx.rootDir), "index")
	idx.store, err = NewStore(idx.ctx, indexDir, embedFn)
	if err != nil {
		return fmt.Errorf("initializing store: %w", err)
	}

	idx.loadManifest()

	idx.setState(StateIndexing)
	t := time.Now()
	fileCount, chunkCount := idx.scanAndIndex()
	fmt.Fprintf(os.Stderr, "[INFO] Initial indexing: %d files, %d chunks in %s\n", fileCount, chunkCount, time.Since(t).Round(time.Millisecond))

	idx.setState(StateIdle)

	if err := idx.startWatcher(); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] File watcher failed: %v\n", err)
	}

	return nil
}

func (idx *Indexer) Stop() {
	idx.cancel()
	if idx.watcher != nil {
		idx.watcher.Close()
	}
	if !idx.externalServers {
		if idx.embedder != nil {
			idx.embedder.Stop()
		}
		if idx.reranker != nil {
			idx.reranker.Stop()
		}
	}
}

func (idx *Indexer) HandleHealth() string {
	return string(idx.getState())
}

func (idx *Indexer) HandleReindex() error {
	idx.setState(StateIndexing)
	defer idx.setState(StateIdle)

	if err := idx.store.Reset(idx.ctx); err != nil {
		return fmt.Errorf("resetting store: %w", err)
	}

	idx.manifest = make(map[string]string)
	idx.signatures = nil
	idx.saveManifest()

	fileCount, chunkCount := idx.scanAndIndex()
	fmt.Fprintf(os.Stderr, "[INFO] Reindex: %d files, %d chunks\n", fileCount, chunkCount)

	return nil
}

func (idx *Indexer) HandleSearchSignature(symbol string) []SignatureEntry {
	var results []SignatureEntry
	for _, s := range idx.signatures {
		if strings.Contains(s.Signature, symbol) {
			results = append(results, s)
		}
	}
	return results
}

func removeFileSignatures(entries []SignatureEntry, filePath string) []SignatureEntry {
	filtered := entries[:0]
	for _, s := range entries {
		if s.FilePath != filePath {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (idx *Indexer) HandleRetrieve(query string) ([]RetrieveResult, error) {
	if idx.getState() != StateIdle {
		return nil, fmt.Errorf("indexer not ready (state: %s)", idx.getState())
	}

	emb, err := idx.embedder.GetEmbeddingOpenAI(idx.ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	candidates, err := idx.store.Search(idx.ctx, emb, idx.searchCount)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	var rerankResults []RerankResult
	if idx.rerankerEnabled {
		docs := make([]string, len(candidates))
		for i, c := range candidates {
			docs[i] = c.Signature + "\n" + c.Content
		}
		rerankResults, err = idx.reranker.Rerank(idx.ctx, query, docs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Reranking failed, using similarity scores: %v\n", err)
			rerankResults = nil
		}
	}

	results := make([]RetrieveResult, len(candidates))
	for i, c := range candidates {
		results[i] = RetrieveResult{
			Source:    "llmdevkit-indexer",
			FilePath:  c.FilePath,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Signature: c.Signature,
			Language:  c.Language,
			ChunkType: c.ChunkType,
			Content:   c.Content,
		}
		if rerankResults != nil {
			for _, rr := range rerankResults {
				if rr.Index == i {
					results[i].Score = rr.RelevanceScore
					break
				}
			}
		} else {
			results[i].Score = float64(c.Similarity)
		}
	}

	if rerankResults != nil {
		sortResults(results)
	}

	if len(results) > idx.resultCount {
		results = results[:idx.resultCount]
	}

	return results, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (idx *Indexer) scanAndIndex() (int, int) {
	fileHashes := make(map[string]string)
	fileCount := 0
	chunkCount := 0

	filepath.Walk(idx.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != idx.rootDir && (SkipDir(info.Name()) || idx.shouldIgnore(path)) {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(idx.rootDir, path)
		if err != nil {
			return nil
		}
		if strings.HasPrefix(relPath, ".llmdevkit") {
			return nil
		}

		if idx.shouldIgnore(path) {
			return nil
		}

		if !ShouldIndex(relPath) {
			return nil
		}

		hash, err := fileHash(path)
		if err != nil {
			return nil
		}
		fileHashes[relPath] = hash

		if stored, ok := idx.manifest[relPath]; ok && stored == hash {
			idx.loadFileSignatures(relPath, path)
			return nil
		}

		if n := idx.indexFile(relPath, path); n > 0 {
			fileCount++
			chunkCount += n
		}
		return nil
	})

	for fp := range idx.manifest {
		if _, ok := fileHashes[fp]; !ok {
			idx.store.RemoveFile(idx.ctx, fp)
			idx.signatures = removeFileSignatures(idx.signatures, fp)
		}
	}

	idx.manifest = fileHashes
	idx.saveManifest()

	return fileCount, chunkCount
}

// loadFileSignatures extracts signatures from a file without re-embedding.
// Used when the file hash matches the manifest (unchanged file) but signatures
// are not in memory (e.g. after indexer restart).
func (idx *Indexer) loadFileSignatures(relPath, absPath string) {
	content, err := os.ReadFile(absPath)
	if err != nil || len(content) == 0 {
		return
	}
	lang := DetectLanguage(relPath)
	if lang == "" {
		return
	}
	chunks, err := ChunkFile(relPath, content, lang)
	if err != nil || len(chunks) == 0 {
		return
	}
	for _, c := range chunks {
		if c.Signature != "" {
			idx.signatures = append(idx.signatures, SignatureEntry{
				FilePath:  c.FilePath,
				LineStart: c.LineStart,
				LineEnd:   c.LineEnd,
				Language:  c.Language,
				ChunkType: c.ChunkType,
				Signature: c.Signature,
			})
		}
	}
}

func (idx *Indexer) indexFile(relPath, absPath string) int {
	content, err := os.ReadFile(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Error reading %s: %v\n", relPath, err)
		return 0
	}

	if len(content) == 0 {
		return 0
	}

	lang := DetectLanguage(relPath)
	if lang == "" {
		return 0
	}

	chunks, err := ChunkFile(relPath, content, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Error chunking %s: %v\n", relPath, err)
		return 0
	}

	if len(chunks) == 0 {
		return 0
	}

	// Remove old signatures for this file
	filtered := idx.signatures[:0]
	for _, s := range idx.signatures {
		if s.FilePath != relPath {
			filtered = append(filtered, s)
		}
	}
	idx.signatures = filtered

	// Collect new signatures
	for _, c := range chunks {
		if c.Signature != "" {
			idx.signatures = append(idx.signatures, SignatureEntry{
				FilePath:  c.FilePath,
				LineStart: c.LineStart,
				LineEnd:   c.LineEnd,
				Language:  c.Language,
				ChunkType: c.ChunkType,
				Signature: c.Signature,
			})
		}
	}

	idx.store.RemoveFile(idx.ctx, relPath)

	if err := idx.store.AddChunks(idx.ctx, chunks); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] Error indexing %s: %v\n", relPath, err)
		return 0
	}

	fmt.Fprintf(os.Stderr, "[INFO] Indexed %s (%d chunks)\n", relPath, len(chunks))
	return len(chunks)
}

func (idx *Indexer) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	idx.watcher = watcher

	filepath.Walk(idx.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != idx.rootDir && SkipDir(info.Name()) {
				return filepath.SkipDir
			}
			watcher.Add(path)
		}
		return nil
	})

	go idx.watchLoop()
	return nil
}

func (idx *Indexer) watchLoop() {
	debounce := make(map[string]time.Time)
	debounceMu := sync.Mutex{}

	for {
		select {
		case <-idx.ctx.Done():
			return
		case event, ok := <-idx.watcher.Events:
			if !ok {
				return
			}
			relPath, err := filepath.Rel(idx.rootDir, event.Name)
			if err != nil {
				continue
			}

			if idx.shouldIgnore(event.Name) {
				continue
			}

			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				if event.Has(fsnotify.Create) && !SkipDir(info.Name()) {
					idx.watcher.Add(event.Name)
				}
				continue
			}

			if !ShouldIndex(relPath) {
				continue
			}

			debounceMu.Lock()
			debounce[relPath] = time.Now()
			debounceMu.Unlock()

		case <-idx.watcher.Errors:
			continue
		}

		select {
		case <-idx.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}

		debounceMu.Lock()
		now := time.Now()
		var toReindex []string
		for fp, t := range debounce {
			if now.Sub(t) >= 2*time.Second {
				toReindex = append(toReindex, fp)
				delete(debounce, fp)
			}
		}
		debounceMu.Unlock()

		if len(toReindex) == 0 {
			continue
		}

		if idx.getState() == StateIdle {
			idx.setState(StateIndexing)
			for _, fp := range toReindex {
				absPath := filepath.Join(idx.rootDir, fp)
				if _, err := os.Stat(absPath); err != nil {
					idx.store.RemoveFile(idx.ctx, fp)
					delete(idx.manifest, fp)
					idx.signatures = removeFileSignatures(idx.signatures, fp)
				} else {
					hash, herr := fileHash(absPath)
					if herr != nil {
						idx.store.RemoveFile(idx.ctx, fp)
						delete(idx.manifest, fp)
						idx.signatures = removeFileSignatures(idx.signatures, fp)
					} else {
						idx.manifest[fp] = hash
						idx.indexFile(fp, absPath)
					}
				}
			}
			idx.saveManifest()
			idx.setState(StateIdle)
		}
	}
}

func (idx *Indexer) setState(s State) {
	idx.stateMu.Lock()
	idx.state = s
	idx.stateMu.Unlock()
}

func (idx *Indexer) getState() State {
	idx.stateMu.RLock()
	defer idx.stateMu.RUnlock()
	return idx.state
}

func (idx *Indexer) loadManifest() {
	path := filepath.Join(cfg.DirPath(idx.rootDir), "index", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &idx.manifest)
}

func (idx *Indexer) saveManifest() {
	path := filepath.Join(cfg.DirPath(idx.rootDir), "index", "manifest.json")
	data, err := json.Marshal(idx.manifest)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0644)
}

func sortResults(results []RetrieveResult) {
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}
