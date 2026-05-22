package main

import (
	"context"
	"fmt"
	"os"

	"llmdevkit/internal/mcps"
	"llmdevkit/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func addOptionalTools(s *server.MCPServer, enableIndexer, enableMemory bool, ignore string) {
	if enableIndexer || enableMemory {
		if err := tools.StartLlamaServers(tools.RootDir, enableMemory); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: llama servers: %v\n", err)
		}
	}

	if enableIndexer {
		if !tools.LlamaReady {
			fmt.Fprintf(os.Stderr, "llmdevkit: indexer: skipped (llama servers not available)\n")
		} else if err := tools.StartIndexer(tools.RootDir, ignore); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: indexer: %v\n", err)
		} else {
			s.AddTool(mcp.NewTool("relevant_code",
				mcp.WithString("prompt",
					mcp.Required(),
				),
			), tools.RelevantCodeHandler)
			s.AddTool(mcp.NewTool("search_symbol_in_code",
				mcp.WithString("symbol_name",
					mcp.Required(),
					mcp.Description("Name only, no types"),
				),
			), tools.SearchSymbolInCodeHandler)
		}
	}

	if enableMemory {
		if !tools.LlamaReady {
			fmt.Fprintf(os.Stderr, "llmdevkit: memory: skipped (llama servers not available)\n")
		} else if err := tools.StartMemory(tools.RootDir); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: memory: %v\n", err)
		} else {
			s.AddTool(mcp.NewTool("memory_put",
				mcp.WithDescription("Add a phrase (fact) to the system"),
				mcp.WithString("fact",
					mcp.Required(),
					mcp.Description("Fact phrase to memorize"),
				),
			), tools.MemoryPutHandler)

			s.AddTool(mcp.NewTool("relevant_memory",
				mcp.WithDescription("Search relevant facts from prompt string"),
				mcp.WithString("prompt",
					mcp.Required(),
				),
			), tools.RelevantMemoryHandler)

			s.AddTool(mcp.NewTool("memory_extract",
				mcp.WithDescription("Extract facts from text and store them in memory, deduplicating against existing facts"),
				mcp.WithString("text",
					mcp.Required(),
					mcp.Description("Text to extract facts from (conversation, notes, document)"),
				),
			), tools.MemoryExtractHandler)
		}
	}
}

func addProxiedTools(s *server.MCPServer, proxiedTools map[string]bool) {
	mcpsCfg, err := mcps.LoadMergedConfig(tools.RootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llmdevkit: mcps config: %v\n", err)
		os.Exit(1)
	}
	if mcpsCfg != nil {
		proxiedNames, err := mcps.RegisterProxiedTools(context.Background(), s, mcpsCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: mcps: %v\n", err)
			os.Exit(1)
		}
		for _, n := range proxiedNames {
			proxiedTools[n] = true
		}
		fmt.Fprintf(os.Stderr, "llmdevkit: loaded %d upstream MCP servers\n", len(mcpsCfg.MCPS))
	}
}
