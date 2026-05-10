package agents

import (
	"fmt"
	"os"
	"strings"

	"llmdevkit/internal/cfg"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agents []AgentConfig `yaml:"agents"`
}

type AgentConfig struct {
	Name         string                       `yaml:"name"`
	LLM          string                       `yaml:"llm"`
	Tools        string                       `yaml:"tools"`
	SystemPrompt string                       `yaml:"system_prompt"`
	Hooks        map[string]map[string]map[string]string `yaml:"hooks,omitempty"`
}

const (
	HookConversationBegin = "on_conversation_begin"
	HookTurnBegin         = "on_turn_begin"
	HookTurnEnd           = "on_turn_end"
	HookConversationEnd   = "on_conversation_end"
)

func ConfigPath(rootDir string) string {
	return cfg.DirPath(rootDir) + "/agents.yml"
}

func GlobalConfigPath() string {
	dp := cfg.GlobalDirPath()
	if dp == "" {
		return ""
	}
	return dp + "/agents.yml"
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
		return nil, fmt.Errorf("parse agents config: %w", err)
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
		for _, a := range globalCfg.Agents {
			if !seen[a.Name] {
				merged.Agents = append(merged.Agents, a)
				seen[a.Name] = true
			}
		}
	}
	if localCfg != nil {
		for _, a := range localCfg.Agents {
			if !seen[a.Name] {
				merged.Agents = append(merged.Agents, a)
				seen[a.Name] = true
			} else {
				// override with local
				for i := range merged.Agents {
					if merged.Agents[i].Name == a.Name {
						merged.Agents[i] = a
						break
					}
				}
			}
		}
	}
	return merged, nil
}

// Lookup returns the agent config by name.
func (c *Config) Lookup(name string) (*AgentConfig, bool) {
	for i := range c.Agents {
		if c.Agents[i].Name == name {
			return &c.Agents[i], true
		}
	}
	return nil, false
}

// Default returns the first agent config.
func (c *Config) Default() *AgentConfig {
	if len(c.Agents) == 0 {
		return nil
	}
	return &c.Agents[0]
}

// ToolNames returns the space-separated tool tokens for the agent.
func (a *AgentConfig) ToolNames() []string {
	return strings.Fields(a.Tools)
}

// HookTools returns the tool invocations for a given hook event.
// Returns map[toolName]map[argName]argValue.
func (a *AgentConfig) HookTools(hook string) map[string]map[string]string {
	if a.Hooks == nil {
		return nil
	}
	h, ok := a.Hooks[hook]
	if !ok || len(h) == 0 {
		return nil
	}
	return h
}
