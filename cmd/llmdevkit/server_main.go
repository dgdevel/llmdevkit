package main

import (
	"embed"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"llmdevkit/internal/agents"
	"llmdevkit/internal/debuglog"
	"llmdevkit/internal/llms"
	"llmdevkit/internal/mcps"
	"llmdevkit/internal/tools"
)

//go:embed ui.html js sw.js
var staticFS embed.FS

// -- Main --------------------------------------------------------------------

func runServer() {
	enableIndexer := flag.Bool("enable-indexer", false, "pass --enable-indexer through to llmdevkit mcp via ACP")
	flag.Parse()

	rootDir, _ := os.Getwd()
	rootDir, _ = filepath.Abs(rootDir)

	tools.RootDir = rootDir

	debuglog.Init(rootDir)
	dlog := debuglog.For("server")
	dlog.Log("server starting, rootDir=%s", rootDir)

	llmCfg, err := llms.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load llms config: %v", err)
	}

	agentCfg, err := agents.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load agents config: %v", err)
	}

	mcpCfg, err := mcps.LoadMergedConfig(rootDir)
	if err != nil {
		log.Fatalf("load mcps config: %v", err)
	}

	srv := &Server{
		rootDir:       rootDir,
		llmCfg:        llmCfg,
		agentCfg:      agentCfg,
		mcpCfg:        mcpCfg,
		dlog:          dlog,
		convs:         make(map[string]*Conversation),
		askPends:      make(map[string]chan *AskAnswer),
		sseClients:    make(map[chan SSEEvent]struct{}),
		toolDefsCache: make(map[string][]ToolDefInfo),
		enableIndexer: *enableIndexer,
	}

	if err := srv.loadConversations(); err != nil {
		log.Printf("warning: load conversations: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveUI)
	mux.HandleFunc("/api/agents", srv.handleAgents)
	mux.HandleFunc("/api/llms", srv.handleLLMs)
	mux.HandleFunc("/api/tooldefs", srv.handleToolDefs)
	mux.HandleFunc("/api/conversations", srv.handleConversations)
	mux.HandleFunc("/api/conversations/", srv.handleConversationActions)
	mux.HandleFunc("/api/ask/", srv.handleAskAnswer)
	mux.HandleFunc("/api/tasks/delete", srv.handleTaskDelete)
	mux.HandleFunc("/api/tasks/clear", srv.handleTasksClear)
	mux.HandleFunc("/api/tasks", srv.handleTasksRead)
	mux.HandleFunc("/api/sidechannel", srv.handleSideChannel)
	mux.HandleFunc("/api/events", srv.handleSSE)
	mux.HandleFunc("/api/notifications", srv.handleNotifications)

	addr := ":18681"

	httpServer := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on SIGINT/SIGTERM.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("shutting down...")
		httpServer.Close()
		debuglog.Close()
	}()

	log.Printf("llmdevkit server listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// -- Static UI ---------------------------------------------------------------

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		data, _ := staticFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
		return
	}
	// Serve JS module files from embedded fs
	if strings.HasPrefix(path, "/js/") && strings.HasSuffix(path, ".js") {
		data, err := staticFS.ReadFile(path[1:]) // strip leading /
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Write(data)
		return
	}
	// Serve service worker
	if path == "/sw.js" {
		data, err := staticFS.ReadFile("sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(data)
		return
	}
	http.NotFound(w, r)
}

// Close kills the ACP subprocess if running.
func (s *Server) Close() {
	s.acpMu.Lock()
	cmd := s.acpCmd
	s.acpMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}
