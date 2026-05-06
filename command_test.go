package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llmdevkit/internal/cfg"

	"github.com/mark3labs/mcp-go/mcp"
)

func setupCommandTest(t *testing.T, config map[string]map[string]string) {
	t.Helper()
	root := t.TempDir()
	rootDir = root
	if config != nil {
		if err := cfg.Write(config, cfg.FilePath(root)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAvailableCommands(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":             "build,test,run",
			"build_cmdline":    "make",
			"build_arguments":  "target",
			"test_cmdline":     "make test",
			"test_description": "Run tests",
			"run_cmdline":      "./executable",
			"run_description":  "Run the main executable; target_folder is the directory to work with, config_file is the reference configuration to use.",
			"run_arguments":    "target_folder, config_file",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "available_commands",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := availableCommandsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("available_commands returned error")
	}

	text := textOf(t, result)
	expected := `Command: build
Arguments: target
Example: make <target>

Command: test
Arguments: no arguments are taken, invoke without arguments
Description: Run tests
Example: make test

Command: run
Arguments: target_folder
Arguments: config_file
Description: Run the main executable; target_folder is the directory to work with, config_file is the reference configuration to use.
Example: ./executable <target_folder> <config_file>
`
	if text != expected {
		t.Errorf("available_commands output:\n%s\nwant:\n%s", text, expected)
	}
}

func TestAvailableCommandsEmpty(t *testing.T) {
	setupCommandTest(t, nil)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "available_commands",
			Arguments: map[string]interface{}{},
		},
	}
	result, err := availableCommandsHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("available_commands returned error")
	}
	text := textOf(t, result)
	if text != "" {
		t.Errorf("expected empty output, got %q", text)
	}
}

func TestExecCommand(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":                "echo_test",
			"echo_test_cmdline":   "echo",
			"echo_test_arguments": "arg1, arg2",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "echo_test",
				"arguments": []interface{}{"hello", "world"},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected output to contain 'hello world', got %q", text)
	}
}

func TestExecCommandWithArgs(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":            "build",
			"build_cmdline":   "echo make",
			"build_arguments": "target",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "build",
				"arguments": []interface{}{"clean"},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "make clean") {
		t.Errorf("expected output to contain 'make clean', got %q", text)
	}
}

func TestExecCommandUnknown(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":          "build",
			"build_cmdline": "make",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "nonexistent",
				"arguments": []interface{}{},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for unknown command")
	}
}

func TestExecCommandWrongArgCount(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":            "build",
			"build_cmdline":   "make",
			"build_arguments": "target",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "build",
				"arguments": []interface{}{},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for wrong argument count")
	}
}

func TestExecCommandNoCmdline(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list": "build",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "build",
				"arguments": []interface{}{},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing cmdline")
	}
}

func TestExecCommandTimeout(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":           "slow",
			"slow_cmdline":   "sleep",
			"slow_arguments": "duration",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "slow",
				"arguments": []interface{}{"30"},
				"timeout":   float64(1),
			},
		},
	}

	start := time.Now()
	result, err := runCommandHandler(context.Background(), req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		text := textOf(t, result)
		if !strings.HasPrefix(text, "Command timed out. Partial output.\n") {
			t.Errorf("expected timeout prefix, got: %q", text)
		}
	}
	if elapsed > 10*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestExecCommandNullByte(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":            "build",
			"build_cmdline":   "echo",
			"build_arguments": "arg",
		},
	})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "build",
				"arguments": []interface{}{"bad\x00arg"},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for null byte in argument")
	}
}

func TestExecCommandStderr(t *testing.T) {
	setupCommandTest(t, map[string]map[string]string{
		"commands": {
			"list":               "err_test",
			"err_test_cmdline":   "/bin/sh",
			"err_test_arguments": "script",
		},
	})

	script := filepath.Join(rootDir, "err.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho stdout_msg\necho stderr_msg >&2"), 0755)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "run_command",
			Arguments: map[string]interface{}{
				"name":      "err_test",
				"arguments": []interface{}{script},
				"timeout":   float64(10),
			},
		},
	}
	result, err := runCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "stdout_msg") || !strings.Contains(text, "stderr_msg") {
		t.Errorf("expected merged stdout+stderr, got: %q", text)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"", nil},
		{",,", nil},
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			}
		}
	}
}
