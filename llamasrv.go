package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"llmdevkit/internal/cfg"
	"llmdevkit/internal/indexer"
)

const defaultRerankerModel = "xinming0111/bge-reranker-base-Q8_0-GGUF"
const defaultRerankerFile = "bge-reranker-base-q8_0.gguf"

var (
	llamaCtx       context.Context
	llamaCancel    context.CancelFunc
	llamaEmbedder  *indexer.LlamaServer
	llamaReranker  *indexer.LlamaServer
	llamaExtractor *indexer.LlamaServer
	llamaMu        sync.Mutex
	llamaReady     bool
)

func startLlamaServers(rootDir string, enableMemory bool) error {
	config := cfg.MergedRead(rootDir)
	llamaCfg := config["llama"]
	if llamaCfg == nil {
		return fmt.Errorf("missing [llama] config section")
	}

	llamaPath := llamaCfg["path"]
	if llamaPath == "" {
		return fmt.Errorf("missing llama.path config")
	}

	embedderRepo := llamaCfg["embedder"]
	if embedderRepo == "" {
		return fmt.Errorf("missing llama.embedder config")
	}

	embedderFlags := strings.Fields(llamaCfg["embedder_flags"])

	ctx, cancel := context.WithCancel(context.Background())
	llamaCtx = ctx
	llamaCancel = cancel

	// Start embedder
	t := time.Now()
	fmt.Fprintf(os.Stderr, "[INFO] Starting embedder server...\n")
	srv, err := indexer.StartServer(ctx, llamaPath, embedderRepo, append(embedderFlags, "--embedding")...)
	if err != nil {
		cancel()
		return fmt.Errorf("starting embedder: %w", err)
	}
	llamaEmbedder = srv
	fmt.Fprintf(os.Stderr, "[INFO] Embedder on port %d (started in %s)\n", srv.Port(), time.Since(t).Round(time.Millisecond))

	// Optionally start reranker
	rerankerEnabled := !cfg.IsDisabled(llamaCfg["reranker_enabled"])
	if rerankerEnabled {
		rerankerRepo := llamaCfg["reranker"]
		if rerankerRepo == "" {
			rerankerRepo = defaultRerankerModel
		}
		rerankerFlags := []string{"--reranking", "--hf-file", defaultRerankerFile}
		t = time.Now()
		fmt.Fprintf(os.Stderr, "[INFO] Starting reranker server (%s)...\n", rerankerRepo)
		rerankerSrv, err := indexer.StartServer(ctx, llamaPath, rerankerRepo, rerankerFlags...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Reranker failed to start: %v\n", err)
		} else {
			llamaReranker = rerankerSrv
			fmt.Fprintf(os.Stderr, "[INFO] Reranker on port %d (started in %s)\n", rerankerSrv.Port(), time.Since(t).Round(time.Millisecond))
		}
	} else {
		fmt.Fprintf(os.Stderr, "[INFO] Reranker disabled\n")
	}

	// Optionally start extractor (for memory)
	if enableMemory {
		extractorRepo := llamaCfg["extractor"]
		if extractorRepo == "" {
			extractorRepo = defaultExtractorModel
		}
		extractorFlags := strings.Fields(llamaCfg["extractor_flags"])

		t = time.Now()
		fmt.Fprintf(os.Stderr, "[INFO] Starting extractor server (%s)...\n", extractorRepo)
		extSrv, err := indexer.StartServer(ctx, llamaPath, extractorRepo, extractorFlags...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] Extractor failed to start (memory extraction disabled): %v\n", err)
		} else {
			llamaExtractor = extSrv
			fmt.Fprintf(os.Stderr, "[INFO] Extractor on port %d (started in %s)\n", extSrv.Port(), time.Since(t).Round(time.Millisecond))
		}
	} else {
		fmt.Fprintf(os.Stderr, "[INFO] Extractor skipped (memory disabled)\n")
	}

	llamaReady = true
	return nil
}

func stopLlamaServers() {
	if llamaCancel != nil {
		llamaCancel()
	}
	if llamaExtractor != nil {
		llamaExtractor.Stop()
	}
	if llamaReranker != nil {
		llamaReranker.Stop()
	}
	if llamaEmbedder != nil {
		llamaEmbedder.Stop()
	}
}
