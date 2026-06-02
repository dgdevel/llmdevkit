package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"llmdevkit/internal/cfg"

	"github.com/mark3labs/mcp-go/mcp"
)

func getCommands() (map[string]string, []string, error) {
	config := cfg.MergedRead(RootDir)
	section, ok := config["commands"]
	if !ok {
		return nil, nil, nil
	}
	listRaw, ok := section["list"]
	if !ok || listRaw == "" {
		return nil, nil, nil
	}
	names := SplitCSV(listRaw)
	return section, names, nil
}

func SplitCSV(s string) []string {
	var result []string
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			result = append(result, v)
		}
	}
	return result
}

func sanitizeArg(arg string) (string, error) {
	if strings.ContainsRune(arg, 0) {
		return "", fmt.Errorf("argument contains null byte")
	}
	return arg, nil
}

func AvailableCommandsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, names, err := getCommands()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(names) == 0 {
		return mcp.NewToolResultText(""), nil
	}
	section, _, _ := getCommands()
	var buf strings.Builder
	for i, name := range names {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("Command: ")
		buf.WriteString(name)
		buf.WriteString("\n")
		descKey := name + "_description"
		argsKey := name + "_arguments"
		if v, ok := section[argsKey]; ok && v != "" {
			args := SplitCSV(v)
			for _, a := range args {
				buf.WriteString("Arguments: ")
				buf.WriteString(a)
				buf.WriteString("\n")
			}
		} else {
			buf.WriteString("Arguments: no arguments are taken, invoke without arguments\n")
		}
		if v, ok := section[descKey]; ok && v != "" {
			buf.WriteString("Description: ")
			buf.WriteString(v)
			buf.WriteString("\n")
		}
		cmdlineKey := name + "_cmdline"
		if v, ok := section[cmdlineKey]; ok && v != "" {
			buf.WriteString("Example: ")
			buf.WriteString(v)
			argsKey := name + "_arguments"
			if a, ok := section[argsKey]; ok && a != "" {
				for _, arg := range SplitCSV(a) {
					buf.WriteString(" <")
					buf.WriteString(strings.TrimSpace(arg))
					buf.WriteString(">")
				}
			}
			buf.WriteString("\n")
		}
	}
	return mcp.NewToolResultText(buf.String()), nil
}

func RunCommandHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var args []string
	if raw, ok := req.Params.Arguments.(map[string]interface{}); ok {
		if a, ok := raw["arguments"]; ok {
			if arr, ok := a.([]interface{}); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok {
						args = append(args, s)
					}
				}
			}
		}
	}
	timeout, err := req.RequireInt("timeout")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	section, names, cfgErr := getCommands()
	if cfgErr != nil {
		return mcp.NewToolResultError(cfgErr.Error()), nil
	}

	found := false
	for _, n := range names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		return mcp.NewToolResultError(fmt.Sprintf("unknown command: %s", name)), nil
	}

	cmdlineKey := name + "_cmdline"
	cmdline, ok := section[cmdlineKey]
	if !ok || cmdline == "" {
		return mcp.NewToolResultError(fmt.Sprintf("command %q has no cmdline configured", name)), nil
	}

	argsKey := name + "_arguments"
	if expected, ok := section[argsKey]; ok && expected != "" {
		expectedArgs := SplitCSV(expected)
		if len(args) != len(expectedArgs) {
			return mcp.NewToolResultError(fmt.Sprintf("command %q expects %d argument(s) (%s), got %d", name, len(expectedArgs), strings.Join(expectedArgs, ", "), len(args))), nil
		}
	} else {
		if len(args) > 0 {
			return mcp.NewToolResultError(fmt.Sprintf("command %q takes no arguments, got %d", name, len(args))), nil
		}
	}

	sanitized := make([]string, len(args))
	for i, a := range args {
		sanitized[i], err = sanitizeArg(a)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid argument %d: %v", i+1, err)), nil
		}
	}

	parts := strings.Fields(cmdline)
	cmdParts := append(parts, sanitized...)

	runDir, runNum, err := NextExecRunDir()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("exec output setup: %v", err)), nil
	}
	execFiles, err := CreateExecOutputFiles(runDir, runNum)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("exec output files: %v", err)), nil
	}
	defer execFiles.Close()

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.Stdout = execFiles.StdoutWriter
	cmd.Stderr = execFiles.StderrWriter

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return mcp.NewToolResultError(MaskPath(fmt.Sprintf("failed to start command: %v", err))), nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timedOut := false
	select {
	case <-done:
	case <-time.After(time.Duration(timeout) * time.Second):
		timedOut = true
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Signal(syscall.SIGKILL)
			<-done
		}
	}

	if timedOut {
		fmt.Fprintln(execFiles.Merged, "Command timed out. Partial output.")
	}

	exitCode := cmd.ProcessState.ExitCode()
	var header string
	if exitCode >= 0 {
		header = fmt.Sprintf("Exit status: %d\nDuration: %.3fs\n", exitCode, time.Since(start).Seconds())
	}

	// Write header to merged output
	fmt.Fprint(execFiles.Merged, header)
	execFiles.Close()

	return mcp.NewToolResultText(header + execFiles.FileMessage()), nil
}
