package tools

import (
	"bytes"
	"fmt"
)

// formatEntrySize formats bytes: <500b -> "Xb", <500Kb -> "XKb", >=500Kb -> "XMb".
func formatEntrySize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
	)
	const threshold = 500
	switch {
	case b >= threshold*MB:
		return fmt.Sprintf("%.0fMb", float64(b)/float64(MB))
	case b >= threshold*KB:
		return fmt.Sprintf("%.0fKb", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%db", b)
	}
}

// isBinary detects binary content by checking for a NUL byte in the first 8KB.
func isBinary(data []byte) bool {
	end := len(data)
	if end > 8192 {
		end = 8192
	}
	return bytes.ContainsRune(data[:end], 0)
}

// countLines returns the number of newlines in data.
func countLines(data []byte) int {
	n := 0
	for _, c := range data {
		if c == '\n' {
			n++
		}
	}
	return n
}
