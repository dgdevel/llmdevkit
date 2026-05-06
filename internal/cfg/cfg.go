package cfg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DirName = ".llmdevkit"
const ConfigFile = "config.ini"
const GlobalDirName = "llmdevkit"

func GlobalBaseDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.config"
}

func GlobalDirPath() string {
	base := GlobalBaseDir()
	if base == "" {
		return ""
	}
	return base + "/" + GlobalDirName
}

func GlobalFilePath() string {
	dp := GlobalDirPath()
	if dp == "" {
		return ""
	}
	return dp + "/" + ConfigFile
}

func DirPath(rootDir string) string {
	return rootDir + "/" + DirName
}

func FilePath(rootDir string) string {
	return DirPath(rootDir) + "/" + ConfigFile
}

func Read(path string) (map[string]map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[string]string{}, nil
		}
		return nil, err
	}
	return Parse(string(data)), nil
}

func Merge(global, local map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	for ns, keys := range global {
		result[ns] = make(map[string]string)
		for k, v := range keys {
			result[ns][k] = v
		}
	}
	for ns, keys := range local {
		if result[ns] == nil {
			result[ns] = make(map[string]string)
		}
		for k, v := range keys {
			result[ns][k] = v
		}
	}
	return result
}

func MergedRead(rootDir string) map[string]map[string]string {
	globalPath := GlobalFilePath()
	global, _ := Read(globalPath)
	if global == nil {
		global = make(map[string]map[string]string)
	}
	localPath := FilePath(rootDir)
	local, _ := Read(localPath)
	if local == nil {
		local = make(map[string]map[string]string)
	}
	return Merge(global, local)
}

func Parse(data string) map[string]map[string]string {
	config := map[string]map[string]string{}
	var section string
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if config[section] == nil {
				config[section] = map[string]string{}
			}
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if config[section] == nil {
				config[section] = map[string]string{}
			}
			config[section][key] = val
		}
	}
	return config
}

func Write(config map[string]map[string]string, path string) error {
	dir := filepath.Dir(path) //nolint:depguard
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var buf strings.Builder
	for section, keys := range config {
		buf.WriteString(fmt.Sprintf("[%s]\n", section))
		for key, val := range keys {
			buf.WriteString(fmt.Sprintf("%s=%s\n", key, val))
		}
	}
	return os.WriteFile(path, []byte(buf.String()), 0644)
}

func ParseBool(val string) bool {
	switch strings.ToLower(val) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func IsReadonly(rootDir string) bool {
	config, err := Read(FilePath(rootDir))
	if err != nil {
		return false
	}
	if core, ok := config["core"]; ok {
		return ParseBool(core["readonly"])
	}
	return false
}

func IsDisabled(val string) bool {
	switch strings.ToLower(val) {
	case "false", "0", "no", "disabled", "off":
		return true
	default:
		return false
	}
}

func Atoi(val string, defaultVal int) int {
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return n
}
