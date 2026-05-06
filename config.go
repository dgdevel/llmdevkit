package main

import (
	"path/filepath"
	"strings"

	"llmdevkit/internal/cfg"
)

func isConfigPath(abs string) bool {
	dir := cfg.DirPath(rootDir)
	if abs == dir || strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return true
	}
	globalDir := cfg.GlobalDirPath()
	if globalDir != "" {
		if abs == globalDir || strings.HasPrefix(abs, globalDir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isReadonly() bool {
	config := cfg.MergedRead(rootDir)
	if core, ok := config["core"]; ok {
		return cfg.ParseBool(core["readonly"])
	}
	return false
}
