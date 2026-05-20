package llms

import (
	"fmt"
	"os"

	"llmdevkit/internal/cfg"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LLMs []LLMConfig `yaml:"llms"`
}

type LLMConfig struct {
	Name        string            `yaml:"name"`
	APIBase     string            `yaml:"api_base"`
	Model       string            `yaml:"model,omitempty"`
	APIKey      string            `yaml:"api_key,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	ContextSize int               `yaml:"context_size,omitempty"`
}

func ConfigPath(rootDir string) string {
	return cfg.DirPath(rootDir) + "/llms.yml"
}

func GlobalConfigPath() string {
	dp := cfg.GlobalDirPath()
	if dp == "" {
		return ""
	}
	return dp + "/llms.yml"
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse llms config: %w", err)
	}
	return &c, nil
}

func LoadMergedConfig(rootDir string) (*Config, error) {
	globalPath := GlobalConfigPath()
	globalCfg, _ := LoadConfig(globalPath)

	localPath := ConfigPath(rootDir)
	localCfg, _ := LoadConfig(localPath)

	if globalCfg == nil && localCfg == nil {
		return nil, nil
	}

	merged := &Config{}
	seen := map[string]bool{}

	if globalCfg != nil {
		for _, l := range globalCfg.LLMs {
			if !seen[l.Name] {
				merged.LLMs = append(merged.LLMs, l)
				seen[l.Name] = true
			}
		}
	}
	if localCfg != nil {
		for _, l := range localCfg.LLMs {
			if !seen[l.Name] {
				merged.LLMs = append(merged.LLMs, l)
				seen[l.Name] = true
			} else {
				// override with local
				for i := range merged.LLMs {
					if merged.LLMs[i].Name == l.Name {
						merged.LLMs[i] = l
						break
					}
				}
			}
		}
	}
	return merged, nil
}

// Lookup returns the LLM config by name from the merged config.
func (c *Config) Lookup(name string) (*LLMConfig, bool) {
	for i := range c.LLMs {
		if c.LLMs[i].Name == name {
			return &c.LLMs[i], true
		}
	}
	return nil, false
}
