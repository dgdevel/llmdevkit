package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var rootDir string
var ignoreGlobs []string

func isIgnored(abs string) bool {
	if len(ignoreGlobs) == 0 {
		return false
	}
	rel, err := filepath.Rel(rootDir, abs)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		for _, g := range ignoreGlobs {
			if ok, _ := filepath.Match(g, part); ok {
				return true
			}
		}
	}
	return false
}

func maskPath(s string) string {
	if rootDir == "" {
		return s
	}
	s = strings.ReplaceAll(s, rootDir+string(os.PathSeparator), string(os.PathSeparator))
	s = strings.ReplaceAll(s, rootDir, string(os.PathSeparator))
	return s
}

func resolve(p string) (string, error) {
	p = filepath.Clean(p)
	if p == "." {
		p = ""
	}
	abs, err := filepath.Abs(filepath.Join(rootDir, p))
	if err != nil {
		return "", err
	}
	r, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	if abs != r && !strings.HasPrefix(abs, r+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root")
	}
	return abs, nil
}

func globMatch(pattern, name string) bool {
	pp := strings.Split(pattern, "/")
	np := strings.Split(name, "/")
	var match func(int, int) bool
	match = func(pi, ni int) bool {
		for pi < len(pp) && ni < len(np) {
			if pp[pi] == "**" {
				pi++
				for i := ni; i <= len(np); i++ {
					if match(pi, i) {
						return true
					}
				}
				return false
			}
			ok, _ := filepath.Match(pp[pi], np[ni])
			if !ok {
				return false
			}
			pi++
			ni++
		}
		for pi < len(pp) && pp[pi] == "**" {
			pi++
		}
		return pi == len(pp) && ni == len(np)
	}
	return match(0, 0)
}

func parseLineRange(s string, total int) (int, int, string) {
	// Empty or ":" means full range
	if s == "" || s == ":" {
		return 0, total, ""
	}

	// Single number like "5" means line 5 to end
	if v, err := strconv.Atoi(s); err == nil {
		if v < 1 {
			return 0, 0, fmt.Sprintf("warning: line_range value %d is out of range (min 1)", v)
		}
		from := v - 1
		var warn string
		if from >= total {
			warn = fmt.Sprintf("warning: line_range start %d exceeds total lines %d", v, total)
			return total, total, warn
		}
		return from, total, ""
	}

	// Formats: from:to, from-to, [from:to], [from-to]
	// Also: :to, from:
	trimmed := strings.Trim(s, "[]")
	sep := ':'
	if strings.Contains(trimmed, "-") {
		sep = '-'
	}
	parts := strings.SplitN(trimmed, string(sep), 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Sprintf("error: invalid line_range syntax %q", s)
	}

	var from, to int
	var warnParts []string

	if parts[0] == "" {
		from = 0
	} else {
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Sprintf("error: invalid line_range syntax %q", s)
		}
		if v < 1 {
			return 0, 0, fmt.Sprintf("error: invalid line_range syntax %q (min line is 1)", s)
		}
		from = v - 1
		if from >= total {
			warnParts = append(warnParts, fmt.Sprintf("start %d exceeds total lines %d", v, total))
			from = total
		}
	}

	if parts[1] == "" {
		to = total
	} else {
		v, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Sprintf("error: invalid line_range syntax %q", s)
		}
		if v < 1 {
			return 0, 0, fmt.Sprintf("error: invalid line_range syntax %q (min line is 1)", s)
		}
		to = v
		if to > total {
			warnParts = append(warnParts, fmt.Sprintf("end %d exceeds total lines %d", v, total))
			to = total
		}
	}

	var warn string
	if len(warnParts) > 0 {
		warn = "warning: " + strings.Join(warnParts, ", ")
	}
	return from, to, warn
}

// isBinary checks if data appears to be binary by looking for null bytes.
func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}
