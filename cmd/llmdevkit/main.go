package main

import (
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "llmdevkit v%s\n\nUsage: llmdevkit <command> [args...]\n\nCommands:\n  config   Manage global and local configuration files\n  setup    Download and configure llama.cpp for embedding/reranking\n  indexer  Build and query the code index\n  mcp      MCP server - file tools, task management, command runner, code search, memory\n  acp      ACP server - agent harness with LLM orchestration, tool routing, and sub-agent invocation\n  server   Web UI server - chat interface for agents\n", version)
		os.Exit(1)
	}

	// Handle --version anywhere
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Println(version)
			os.Exit(0)
		}
	}

	command := os.Args[1]
	// Shift args so flag.Parse() in subcommands works naturally
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch strings.ToLower(command) {
	case "config":
		runConfig()
	case "setup":
		runSetup()
	case "indexer":
		runIndexer()
	case "mcp":
		runMCP()
	case "acp":
		runACP()
	case "server":
		runServer()
	default:
		fmt.Fprintf(os.Stderr, "llmdevkit: unknown command %q\n", command)
		os.Exit(1)
	}
}
