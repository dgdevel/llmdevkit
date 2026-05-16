package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"llmdevkit/internal/indexer"
)

func runIndexer() {
	embedderPort := flag.Int("embedder-port", 0, "port of pre-started embedder server (0 = start own)")
	rerankerPort := flag.Int("reranker-port", 0, "port of pre-started reranker server (0 = none)")
	ignoreF := flag.String("ignore", "", "comma-separated glob patterns to ignore files/directories")
	flag.Parse()

	var ignoreGlobs []string
	if *ignoreF != "" {
		ignoreGlobs = strings.Split(*ignoreF, ",")
	}

	args := flag.Args()
	rootDir := "."
	if len(args) > 0 {
		rootDir = args[0]
	}
	rootDir, _ = filepath.Abs(rootDir)

	var idx *indexer.Indexer
	if *embedderPort > 0 {
		embedder := indexer.NewLlamaServerOnPort(*embedderPort)
		var reranker *indexer.LlamaServer
		if *rerankerPort > 0 {
			reranker = indexer.NewLlamaServerOnPort(*rerankerPort)
		}
		idx = indexer.NewIndexerWithServers(rootDir, embedder, reranker, ignoreGlobs)
	} else {
		idx = indexer.NewIndexer(rootDir, ignoreGlobs)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		idx.Stop()
		os.Exit(0)
	}()

	start := time.Now()
	fmt.Fprintf(os.Stderr, "[INFO] Starting indexer for %s\n", rootDir)
	if err := idx.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[INFO] Indexer ready in %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Println("idle")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := parts[0]

		switch cmd {
		case "health":
			fmt.Println(idx.HandleHealth())

		case "reindex":
			t := time.Now()
			if err := idx.HandleReindex(); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] reindex: %v\n", err)
				fmt.Printf("error: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[INFO] Reindex completed in %s\n", time.Since(t).Round(time.Millisecond))
				fmt.Println("ok")
			}

		case "retrieve":
			if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
				fmt.Println("error: missing query text")
				continue
			}
			query := parts[1]
			t := time.Now()
			results, err := idx.HandleRetrieve(query)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] retrieve: %v\n", err)
				fmt.Printf("error: %v\n", err)
				continue
			}
			data, _ := json.Marshal(results)
			fmt.Println(string(data))
			fmt.Fprintf(os.Stderr, "[INFO] retrieve completed in %s (%d results)\n", time.Since(t).Round(time.Millisecond), len(results))

		case "search_signature":
			if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
				fmt.Println("error: missing symbol name")
				continue
			}
			symbol := parts[1]
			results := idx.HandleSearchSignature(symbol)
			data, _ := json.Marshal(results)
			fmt.Println(string(data))

		default:
			fmt.Printf("error: unknown command: %s\n", cmd)
		}
	}

	idx.Stop()
}
