package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llmdevkit/internal/cfg"
)

func runConfig() {
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: llmdevkit config [--global] <get|set> <namespace.key> [value]")
		fmt.Fprintln(os.Stderr, "       llmdevkit config [--global] <root> <get|set> <namespace.key> [value]")
		os.Exit(1)
	}

	useGlobal := false
	if args[0] == "--global" {
		useGlobal = true
		args = args[1:]
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: llmdevkit config [--global] <get|set> <namespace.key> [value]")
			os.Exit(1)
		}
	}

	var configPath string
	var cmdArgs []string

	if args[0] == "get" || args[0] == "set" {
		if useGlobal {
			configPath = cfg.GlobalFilePath()
		} else {
			rootDir, _ := filepath.Abs(".")
			configPath = cfg.FilePath(rootDir)
		}
		cmdArgs = args
	} else {
		if useGlobal {
			fmt.Fprintln(os.Stderr, "error: cannot specify root directory with --global")
			os.Exit(1)
		}
		rootDir := args[0]
		rootDir, _ = filepath.Abs(rootDir)
		configPath = cfg.FilePath(rootDir)
		cmdArgs = args[1:]
	}

	if len(cmdArgs) < 1 {
		fmt.Fprintln(os.Stderr, "missing command")
		os.Exit(1)
	}

	cmd := cmdArgs[0]
	parts := strings.SplitN(cmdArgs[1], ".", 2)
	if len(parts) != 2 {
		fmt.Fprintln(os.Stderr, "key must be in namespace.key format")
		os.Exit(1)
	}
	namespace, k := parts[0], parts[1]

	switch cmd {
	case "get":
		config, err := cfg.Read(configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if ns, ok := config[namespace]; ok {
			if v, ok := ns[k]; ok {
				fmt.Println(v)
				return
			}
		}
		os.Exit(1)
	case "set":
		if len(cmdArgs) < 3 {
			fmt.Fprintln(os.Stderr, "missing value")
			os.Exit(1)
		}
		val := cmdArgs[2]
		config, err := cfg.Read(configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if config[namespace] == nil {
			config[namespace] = map[string]string{}
		}
		config[namespace][k] = val
		if err := cfg.Write(config, configPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
