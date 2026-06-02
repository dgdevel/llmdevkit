package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	execCounter   int
	execCounterMu sync.Mutex
	execDirInit   bool
	execDirMu     sync.Mutex
)

// ResetExecState resets the exec output state (counter and init flag).
// Intended for testing.
func ResetExecState() {
	execCounterMu.Lock()
	execCounter = 0
	execCounterMu.Unlock()
	execDirMu.Lock()
	execDirInit = false
	execDirMu.Unlock()
}

// ExecsDir returns the .llmdevkit/execs directory path under RootDir.
func ExecsDir() string {
	return filepath.Join(RootDir, ".llmdevkit", "execs")
}

// NextExecRunDir creates the next run-X directory and returns its path and the run number.
// On the very first call it empties the execs directory.
func NextExecRunDir() (string, int, error) {
	execDirMu.Lock()
	defer execDirMu.Unlock()

	dir := ExecsDir()
	if !execDirInit {
		// First run: create (or recreate) and empty the directory
		if err := os.RemoveAll(dir); err != nil {
			return "", 0, fmt.Errorf("clean execs dir: %w", err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", 0, fmt.Errorf("create execs dir: %w", err)
		}
		execDirInit = true
	} else {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", 0, fmt.Errorf("create execs dir: %w", err)
		}
	}

	execCounterMu.Lock()
	execCounter++
	num := execCounter
	execCounterMu.Unlock()

	runDir := filepath.Join(dir, fmt.Sprintf("run-%d", num))
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return "", 0, fmt.Errorf("create run dir: %w", err)
	}
	return runDir, num, nil
}

// ExecOutputWriters creates stdout, stderr, and merged-output files in runDir
// and returns writers for each. The merged writer receives all output in sync.
// Callers must close all writers when done.
type ExecOutputFiles struct {
	Stdout       *os.File
	Stderr       *os.File
	Merged       *os.File
	StdoutWriter io.Writer
	StderrWriter io.Writer
	RunDir       string
	RunNum       int
}

// CreateExecOutputFiles sets up stdout.txt, stderr.txt, merged-output.txt in runDir.
func CreateExecOutputFiles(runDir string, runNum int) (*ExecOutputFiles, error) {
	stdoutPath := filepath.Join(runDir, "stdout.txt")
	stderrPath := filepath.Join(runDir, "stderr.txt")
	mergedPath := filepath.Join(runDir, "merged-output.txt")

	stdoutF, err := os.Create(stdoutPath)
	if err != nil {
		return nil, fmt.Errorf("create stdout.txt: %w", err)
	}
	stderrF, err := os.Create(stderrPath)
	if err != nil {
		stdoutF.Close()
		return nil, fmt.Errorf("create stderr.txt: %w", err)
	}
	mergedF, err := os.Create(mergedPath)
	if err != nil {
		stdoutF.Close()
		stderrF.Close()
		return nil, fmt.Errorf("create merged-output.txt: %w", err)
	}

	return &ExecOutputFiles{
		Stdout:       stdoutF,
		Stderr:       stderrF,
		Merged:       mergedF,
		StdoutWriter: io.MultiWriter(stdoutF, mergedF),
		StderrWriter: io.MultiWriter(stderrF, mergedF),
		RunDir:       runDir,
		RunNum:       runNum,
	}, nil
}

// Close closes all output files.
func (e *ExecOutputFiles) Close() {
	if e.Stdout != nil {
		e.Stdout.Close()
	}
	if e.Stderr != nil {
		e.Stderr.Close()
	}
	if e.Merged != nil {
		e.Merged.Close()
	}
}

// ExecRunRelPath returns the relative path of the run directory (e.g. ".llmdevkit/execs/run-3").
func (e *ExecOutputFiles) RelPath() string {
	return filepath.Join(".llmdevkit", "execs", fmt.Sprintf("run-%d", e.RunNum))
}

// ExecFileMessage returns the message to send to the agent instead of raw output.
func (e *ExecOutputFiles) FileMessage() string {
	rel := e.RelPath()
	return fmt.Sprintf("If needed use file_read to read the output files: %s/stdout.txt, %s/stderr.txt, %s/merged-output.txt",
		rel, rel, rel)
}

// IsExecsPath checks if abs path is under the .llmdevkit/execs directory.
func IsExecsPath(abs string) bool {
	execsDir := ExecsDir()
	if abs == execsDir {
		return true
	}
	if strings.HasPrefix(abs, execsDir+string(os.PathSeparator)) {
		return true
	}
	return false
}
