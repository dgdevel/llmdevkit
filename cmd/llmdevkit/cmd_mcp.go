package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llmdevkit/internal/cfg"
	"llmdevkit/internal/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func runMCP() {
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

	args := flag.Args()
	if len(args) > 0 {
		tools.RootDir = args[0]
	} else {
		tools.RootDir, _ = os.Getwd()
	}
	tools.RootDir, _ = filepath.Abs(tools.RootDir)

	// load ignore patterns from config [mcp] section; CLI flag overrides
	if ignore == "" {
		config := cfg.MergedRead(tools.RootDir)
		if mcpCfg, ok := config["mcp"]; ok {
			if v := mcpCfg["ignore"]; v != "" {
				ignore = v
			}
		}
	}

	if ignore != "" {
		tools.IgnoreGlobs = tools.SplitCSV(ignore)
		for _, g := range tools.IgnoreGlobs {
			if _, err := filepath.Match(g, ""); err != nil {
				fmt.Fprintf(os.Stderr, "llmdevkit: invalid ignore pattern %q: %v\n", g, err)
				os.Exit(1)
			}
		}
	}

	proxiedTools := map[string]bool{}

	s := server.NewMCPServer("llmdevkit", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithToolFilter(func(ctx context.Context, tlist []mcp.Tool) []mcp.Tool {
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
			for _, t := range tlist {
				if proxiedTools[t.Name] {
					filtered = append(filtered, t)
					continue
				}
				if tools.IsReadonly() && readonlyHidden[t.Name] {
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

	addCoreTools(s)
	addOptionalTools(s, enableIndexer, enableMemory, ignore)
	addProxiedTools(s, proxiedTools)

	if enableIndexer || enableMemory {
		defer tools.StopLlamaServers()
	}

	if http && !stdio {
		srv := server.NewStreamableHTTPServer(s)
		fmt.Fprintf(os.Stderr, "llmdevkit: HTTP on %s, root=%s\n", addr, tools.RootDir)
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
