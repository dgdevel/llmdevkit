package tools

import (
	"path/filepath"
	"strings"

	"llmdevkit/internal/cfg"
)

func IsConfigPath(abs string) bool {
	if IsExecsPath(abs) {
		return false
	}
	dir := cfg.DirPath(RootDir)
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

func IsReadonly() bool {
	config := cfg.MergedRead(RootDir)
	if core, ok := config["core"]; ok {
		return cfg.ParseBool(core["readonly"])
	}
	return false
}
