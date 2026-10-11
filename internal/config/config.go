// Package config loads the user's settings and builds the list of agents.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"

	"github.com/henomis/cicoria/internal/agent"
)

// Config is the content of config.toml.
type Config struct {
	// Prefix is the key that starts an app command.
	Prefix string `toml:"prefix"`
	// DefaultAgent is the agent started when cicoria opens with no sessions.
	DefaultAgent string `toml:"default_agent"`
	// Notify enables desktop notifications when a session needs input.
	Notify bool `toml:"notify"`
	// LeftPanel and RightPanel say whether cicoria opens with the panels on
	// that side shown: sessions and files on the left, changes and the queue
	// on the right.
	LeftPanel  bool `toml:"left_panel"`
	RightPanel bool `toml:"right_panel"`
	// Agents are command lines to offer as agents, by name. An entry named
	// "claude" changes how Claude Code is started.
	Agents map[string]AgentConfig `toml:"agents"`
}

// AgentConfig describes one agent.
type AgentConfig struct {
	Command []string `toml:"command"`
}

// Default returns the settings used when there is no config file.
func Default() Config {
	return Config{Prefix: "ctrl+a", DefaultAgent: "claude", Notify: true, LeftPanel: true, RightPanel: true}
}

// Path returns where the config file is looked for.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cicoria", "config.toml")
}

// Load reads the config file at path. A missing file gives the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return cfg, fmt.Errorf("%s: unknown setting %q", path, u[0].String())
	}
	for name, a := range cfg.Agents {
		if len(a.Command) == 0 {
			return cfg, fmt.Errorf("%s: agent %q has no command", path, name)
		}
	}
	return cfg, nil
}

// Adapters returns the agents to offer: Claude Code first, then the
// configured ones by name, then a plain shell.
func (c Config) Adapters() []agent.Adapter {
	var claude []string
	if a, ok := c.Agents["claude"]; ok {
		claude = a.Command
	}
	adapters := []agent.Adapter{agent.NewClaude(claude)}

	names := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		if name != "claude" && name != "shell" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		adapters = append(adapters, agent.NewGeneric(name, c.Agents[name].Command))
	}

	shell := []string{os.Getenv("SHELL")}
	if a, ok := c.Agents["shell"]; ok {
		shell = a.Command
	} else if shell[0] == "" {
		shell[0] = "sh"
	}
	return append(adapters, agent.NewGeneric("shell", shell))
}
