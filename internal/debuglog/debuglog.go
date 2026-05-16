package debuglog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"llmdevkit/internal/cfg"
)

var (
	mu       sync.Mutex
	loggers  map[string]*log.Logger
	active   bool
	rootDir  string
	llmFile  *os.File
	llmMu    sync.Mutex
)

func init() {
	loggers = make(map[string]*log.Logger)
}

// CheckEnabled reads merged config and returns true if [core] debug=true.
func CheckEnabled(root string) bool {
	c := cfg.MergedRead(root)
	if core, ok := c["core"]; ok {
		return cfg.ParseBool(core["debug"])
	}
	return false
}

// Init initializes debug logging for the given rootDir.
// Reads config to check if debug is enabled. If enabled, opens log files
// in .llmdevkit/logs/. Call once at program start.
func Init(root string) {
	mu.Lock()
	defer mu.Unlock()
	rootDir = root
	active = CheckEnabled(root)
	log.Printf("debuglog.Init root=%s active=%v", root, active)
	if active {
		logDir := filepath.Join(root, ".llmdevkit", "logs")
		os.MkdirAll(logDir, 0755)
		log.Printf("debug logging enabled, logs dir: %s", logDir)
		// Open dedicated LLM log file.
		llmPath := filepath.Join(logDir, "llm.log")
		f, err := os.OpenFile(llmPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			log.Printf("debuglog: cannot open %s: %v", llmPath, err)
		} else {
			llmFile = f
		}
	}
}

// Enabled returns whether debug logging is active.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return active
}

// LLMLog writes a raw LLM request or response payload to llm.log.
// Each entry is prefixed with a separator containing direction and timestamp.
// The payload itself is written untouched.
func LLMLog(direction, payload string) {
	llmMu.Lock()
	defer llmMu.Unlock()
	if llmFile == nil {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000")
	fmt.Fprintf(llmFile, "======== %s %s ========\n%s\n", direction, ts, payload)
}

// For returns a namespaced logger. Each name gets its own log file.
func For(name string) *Logger {
	mu.Lock()
	defer mu.Unlock()
	if !active {
		return &Logger{w: io.Discard}
	}
	if l, ok := loggers[name]; ok {
		return &Logger{w: l.Writer()}
	}
	logDir := filepath.Join(rootDir, ".llmdevkit", "logs")
	path := filepath.Join(logDir, name+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("debuglog: cannot open %s: %v", path, err)
		return &Logger{w: io.Discard}
	}
	l := log.New(f, "", 0)
	loggers[name] = l
	return &Logger{w: f, logger: l}
}

// Logger writes timestamped entries to a named log file.
type Logger struct {
	w      io.Writer
	logger *log.Logger
}

// Log writes a timestamped log entry.
func (l *Logger) Log(format string, args ...interface{}) {
	if l.logger == nil {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000")
	l.logger.Printf("[%s] %s", ts, fmt.Sprintf(format, args...))
}

// ReqRes logs a request/response pair.
func (l *Logger) ReqRes(direction, payload string) {
	if l.logger == nil {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000")
	l.logger.Printf("[%s] %s\n%s", ts, direction, payload)
}
