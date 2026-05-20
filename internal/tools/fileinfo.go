package tools

import (
	"bytes"
	"fmt"
)

// formatEntrySize formats bytes with thresholds at half of next unit.
// < 512b → exact int + "b", < 512Kb → float with 2 decimals + "Kb", < 512Mb → "Mb".
func formatEntrySize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB/2:
		return fmt.Sprintf("%.2f Mb", float64(b)/float64(MB))
	case b >= MB/2:
		return fmt.Sprintf("%.2f Kb", float64(b)/float64(KB))
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
