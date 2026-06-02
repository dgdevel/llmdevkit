package tools

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
	RootDir = root
	ResetExecState()
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
	result, err := AvailableCommandsHandler(context.Background(), req)
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
	result, err := AvailableCommandsHandler(context.Background(), req)
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
	result, err := RunCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "file_read") || !strings.Contains(text, "run-") {
		t.Errorf("expected file_read message with run dir, got %q", text)
	}
	// Verify output was written to file
	stdoutData, err := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "stdout.txt"))
	if err != nil {
		t.Fatalf("failed to read stdout.txt: %v", err)
	}
	if !strings.Contains(string(stdoutData), "hello world") {
		t.Errorf("expected stdout.txt to contain 'hello world', got %q", string(stdoutData))
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
	result, err := RunCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "file_read") || !strings.Contains(text, "run-") {
		t.Errorf("expected file_read message with run dir, got %q", text)
	}
	// Verify output was written to file
	stdoutData, err := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "stdout.txt"))
	if err != nil {
		t.Fatalf("failed to read stdout.txt: %v", err)
	}
	if !strings.Contains(string(stdoutData), "make clean") {
		t.Errorf("expected stdout.txt to contain 'make clean', got %q", string(stdoutData))
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
	result, err := RunCommandHandler(context.Background(), req)
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
	result, err := RunCommandHandler(context.Background(), req)
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
	result, err := RunCommandHandler(context.Background(), req)
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
	result, err := RunCommandHandler(context.Background(), req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		text := textOf(t, result)
		if !strings.Contains(text, "file_read") || !strings.Contains(text, "run-") {
			t.Errorf("expected file_read message with run dir, got: %q", text)
		}
		// Verify merged output contains timeout message
		mergedData, err := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "merged-output.txt"))
		if err != nil {
			t.Fatalf("failed to read merged-output.txt: %v", err)
		}
		if !strings.Contains(string(mergedData), "Command timed out") {
			t.Errorf("expected merged output to contain timeout message, got %q", string(mergedData))
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
	result, err := RunCommandHandler(context.Background(), req)
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

	script := filepath.Join(RootDir, "err.sh")
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
	result, err := RunCommandHandler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exec_command returned error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if !strings.Contains(text, "file_read") || !strings.Contains(text, "run-") {
		t.Errorf("expected file_read message with run dir, got %q", text)
	}
	// Verify merged output contains both stdout and stderr
	mergedData, err := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "merged-output.txt"))
	if err != nil {
		t.Fatalf("failed to read merged-output.txt: %v", err)
	}
	if !strings.Contains(string(mergedData), "stdout_msg") || !strings.Contains(string(mergedData), "stderr_msg") {
		t.Errorf("expected merged output to contain stdout_msg and stderr_msg, got %q", string(mergedData))
	}
	// Also check separate files
	stdoutData, _ := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "stdout.txt"))
	stderrData, _ := os.ReadFile(filepath.Join(RootDir, ".llmdevkit", "execs", "run-1", "stderr.txt"))
	if !strings.Contains(string(stdoutData), "stdout_msg") {
		t.Errorf("expected stdout.txt to contain stdout_msg, got %q", string(stdoutData))
	}
	if !strings.Contains(string(stderrData), "stderr_msg") {
		t.Errorf("expected stderr.txt to contain stderr_msg, got %q", string(stderrData))
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
		got := SplitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("SplitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("SplitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			}
		}
	}
}
