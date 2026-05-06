package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llmdevkit/internal/mcps"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var (
		stdio         bool
		http          bool
		addr          string
		ignore        string
		showTools     string
		hideTools     string
		enableIndexer bool
		enableMemory  bool
	)
	{
		stdioF := flag.Bool("stdio", false, "use stdio transport")
		httpF := flag.Bool("http", false, "use HTTP transport")
		addrF := flag.String("address", "localhost:8080", "HTTP listen address")
		ignoreF := flag.String("ignore", "", "comma-separated glob patterns to ignore files/directories")
		showF := flag.String("show", "", "comma-separated whitelist of tool names (mutually exclusive with -hide)")
		hideF := flag.String("hide", "", "comma-separated blacklist of tool names (mutually exclusive with -show)")
		indexerF := flag.Bool("enable-indexer", false, "start code indexer subprocess")
		memoryF := flag.Bool("enable-memory", false, "start memory subsystem with embedder")
		flag.Parse()
		stdio, http, addr, ignore, showTools, hideTools, enableIndexer, enableMemory = *stdioF, *httpF, *addrF, *ignoreF, *showF, *hideF, *indexerF, *memoryF
	}

	if showTools != "" && hideTools != "" {
		fmt.Fprintln(os.Stderr, "llmdevkit: --show and --hide are mutually exclusive")
		os.Exit(1)
	}

	if ignore != "" {
		ignoreGlobs = splitCSV(ignore)
		for _, g := range ignoreGlobs {
			if _, err := filepath.Match(g, ""); err != nil {
				fmt.Fprintf(os.Stderr, "llmdevkit: invalid ignore pattern %q: %v\n", g, err)
				os.Exit(1)
			}
		}
	}

	args := flag.Args()
	if len(args) > 0 {
		rootDir = args[0]
	} else {
		rootDir, _ = os.Getwd()
	}
	rootDir, _ = filepath.Abs(rootDir)

	proxiedTools := map[string]bool{}

	s := server.NewMCPServer("llmdevkit", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithToolFilter(func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
			readonlyHidden := map[string]bool{
				"file_create": true, "sed": true, "edit": true, "rm": true, "mv": true,
			}

			var showSet map[string]bool
			var hideSet map[string]bool
			if showTools != "" {
				showSet = make(map[string]bool)
				for _, n := range strings.Split(showTools, ",") {
					showSet[strings.TrimSpace(n)] = true
				}
			}
			if hideTools != "" {
				hideSet = make(map[string]bool)
				for _, n := range strings.Split(hideTools, ",") {
					hideSet[strings.TrimSpace(n)] = true
				}
			}

			var filtered []mcp.Tool
			for _, t := range tools {
				if proxiedTools[t.Name] {
					filtered = append(filtered, t)
					continue
				}
				if isReadonly() && readonlyHidden[t.Name] {
					continue
				}
				if showSet != nil && !showSet[t.Name] {
					continue
				}
				if hideSet != nil && hideSet[t.Name] {
					continue
				}
				filtered = append(filtered, t)
			}
			return filtered
		}),
	)

	s.AddTool(mcp.NewTool("ls",
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), lsHandler)

	s.AddTool(mcp.NewTool("file_read",
		mcp.WithDescription("Read file"),
		mcp.WithString("path",
			mcp.Required(),
		),
		mcp.WithString("line_range",
			mcp.Required(),
			mcp.Description("Line range, 1-indexed. Formats: from:to, from-to, [from:to], [from-to]. Examples: 1:10, 1-10, [1-10], [1:10]"),
		),
	), fileReadHandler)

	s.AddTool(mcp.NewTool("file_create",
		mcp.WithString("path",
			mcp.Required(),
		),
		mcp.WithString("content",
			mcp.Required(),
		),
	), createHandler)

	s.AddTool(mcp.NewTool("mv",
		mcp.WithDescription("Move files"),
		mcp.WithString("source",
			mcp.Required(),
		),
		mcp.WithString("dest",
			mcp.Required(),
		),
	), mvHandler)

	s.AddTool(mcp.NewTool("grep",
		mcp.WithDescription("Print lines matching pattern with context (`grep -A1 -B1`)"),
		mcp.WithString("pattern",
			mcp.Required(),
			mcp.Description("Regexp"),
		),
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), grepHandler)

	s.AddTool(mcp.NewTool("sed",
		mcp.WithDescription("Search and replace in files (`sed -i`)"),
		mcp.WithString("pattern",
			mcp.Required(),
			mcp.Description("Regexp"),
		),
		mcp.WithString("replacement",
			mcp.Required(),
		),
		mcp.WithString("pathspec",
			mcp.Required(),
			mcp.Description("Glob expression for file names"),
		),
	), sedHandler)

	s.AddTool(mcp.NewTool("edit",
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("File path"),
		),
		mcp.WithNumber("start_line_number",
			mcp.Required(),
			mcp.Description("Line number where original_window begins (1-indexed)"),
		),
		mcp.WithString("original_window",
			mcp.Required(),
			mcp.Description("Text to be replaced"),
		),
		mcp.WithString("modified_window",
			mcp.Required(),
			mcp.Description("Text to be inserted"),
		),
	), editHandler)

	s.AddTool(mcp.NewTool("rm",
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("File path"),
		),
	), rmHandler)

	s.AddTool(mcp.NewTool("stat",
		mcp.WithDescription("Infos on files and directories"),
		mcp.WithString("path",
			mcp.Required(),
		),
	), statHandler)

	s.AddTool(mcp.NewTool("tasks_list",
		mcp.WithDescription("List of tasks ([ ] created, [_] in progress, [X] completed)"),
	), tasksListHandler)

	s.AddTool(mcp.NewTool("task_create",
		mcp.WithString("description",
			mcp.Required(),
		),
		mcp.WithString("parent",
			mcp.Description("ID of parent task, optional"),
		),
	), tasksCreateHandler)

	s.AddTool(mcp.NewTool("task_set_status",
		mcp.WithDescription("Change status of task"),
		mcp.WithString("ID",
			mcp.Required(),
			mcp.Description("Task ID"),
		),
		mcp.WithString("status",
			mcp.Required(),
			mcp.Description("One of: created, in_progress, completed"),
		),
	), tasksSetStatusHandler)

	s.AddTool(mcp.NewTool("task_delete",
		mcp.WithString("ID",
			mcp.Required(),
		),
	), tasksDeleteHandler)

	s.AddTool(mcp.NewTool("tasks_clear",
		mcp.WithDescription("Clear all tasks"),
	), tasksClearHandler)

	s.AddTool(mcp.NewTool("w3m-dump",
		mcp.WithDescription("Fetch a webpage text (like `w3m -dump`)"),
		mcp.WithString("url",
			mcp.Required(),
		),
	), w3mdumpHandler)

	s.AddTool(mcp.NewTool("online_search",
		mcp.WithDescription("Search online"),
		mcp.WithString("search_query",
			mcp.Required(),
		),
	), onlineSearchHandler)

	s.AddTool(mcp.NewTool("examples",
		mcp.WithDescription("Show usage examples for a tool"),
		mcp.WithString("tool_name",
			mcp.Required(),
			mcp.Description("Name of the tool to get examples for"),
		),
	), examplesHandler)

	s.AddTool(mcp.NewTool("available_commands",
		mcp.WithDescription("List available commands"),
	), availableCommandsHandler)

	s.AddTool(mcp.NewTool("run_command",
		mcp.WithDescription("Run the command from available_commands"),
		mcp.WithString("name",
			mcp.Required(),
		),
		mcp.WithArray("arguments",
			mcp.Description("Array of strings to pass to the command line"),
			mcp.WithStringItems(),
		),
		mcp.WithNumber("timeout",
			mcp.Required(),
			mcp.Description("Timeout in seconds"),
		),
	), runCommandHandler)

	if enableIndexer || enableMemory {
		if err := startLlamaServers(rootDir, enableMemory); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: llama servers: %v\n", err)
		}
		defer stopLlamaServers()
	}

	if enableIndexer {
		if !llamaReady {
			fmt.Fprintf(os.Stderr, "llmdevkit: indexer: skipped (llama servers not available)\n")
		} else if err := startIndexer(rootDir, ignore); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: indexer: %v\n", err)
		} else {
			s.AddTool(mcp.NewTool("relevant_code",
				mcp.WithString("prompt",
					mcp.Required(),
				),
			), relevantCodeHandler)
			s.AddTool(mcp.NewTool("search_symbol_in_code",
				mcp.WithString("symbol_name",
					mcp.Required(),
				),
			), searchSymbolInCodeHandler)
		}
	}

	if enableMemory {
		if !llamaReady {
			fmt.Fprintf(os.Stderr, "llmdevkit: memory: skipped (llama servers not available)\n")
		} else if err := startMemory(rootDir); err != nil {
			fmt.Fprintf(os.Stderr, "llmdevkit: memory: %v\n", err)
		} else {
			s.AddTool(mcp.NewTool("memory_put",
				mcp.WithDescription("Add a phrase (fact) to the system"),
				mcp.WithString("fact",
					mcp.Required(),
					mcp.Description("Fact phrase to memorize"),
				),
			), memoryPutHandler)

			s.AddTool(mcp.NewTool("relevant_memory",
				mcp.WithDescription("Search relevant facts from prompt string"),
				mcp.WithString("prompt",
					mcp.Required(),
				),
			), relevantMemoryHandler)

			s.AddTool(mcp.NewTool("memory_extract",
				mcp.WithDescription("Extract facts from text and store them in memory, deduplicating against existing facts"),
				mcp.WithString("text",
					mcp.Required(),
					mcp.Description("Text to extract facts from (conversation, notes, document)"),
				),
			), memoryExtractHandler)
		}
	}

	mcpsCfg, err := mcps.LoadMergedConfig(rootDir)
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

	if http && !stdio {
		srv := server.NewStreamableHTTPServer(s)
		fmt.Fprintf(os.Stderr, "llmdevkit: HTTP on %s, root=%s\n", addr, rootDir)
		if err := srv.Start(addr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
