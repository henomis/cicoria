// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	// Changes says what the changes of a session are when cicoria opens:
	// ChangesUncommitted or ChangesSession.
	Changes string `toml:"changes"`
	// IgnoredFiles says whether the files panel opens with the files git
	// ignores shown.
	IgnoredFiles bool `toml:"ignored_files"`
	// CloseOnExit closes a session when its agent's process ends. Without
	// it the session stays, to be restarted or closed by hand.
	CloseOnExit bool `toml:"close_on_exit"`
	// Animations enables the ASCII art that plays when cicoria opens and
	// quits and when a session is created or closed.
	Animations bool `toml:"animations"`
	// Agents are command lines to offer as agents, by name. An entry named
	// "claude" or "copilot" changes how Claude Code or Copilot CLI is
	// started.
	Agents map[string]AgentConfig `toml:"agents"`
}

// What the changes panel can show.
const (
	// ChangesUncommitted is what is not committed.
	ChangesUncommitted = "uncommitted"
	// ChangesSession is everything since the session started, committed
	// or not.
	ChangesSession = "session"
)

// AgentConfig describes one agent.
type AgentConfig struct {
	Command []string `toml:"command"`
}

// Default returns the settings used when there is no config file.
func Default() Config {
	return Config{Prefix: "ctrl+a", DefaultAgent: "claude", Notify: true, LeftPanel: true, RightPanel: true, Changes: ChangesUncommitted, IgnoredFiles: true, CloseOnExit: true, Animations: true}
}

// Path returns where the config file is looked for: under $XDG_CONFIG_HOME
// or ~/.config, on every system.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "cicoria", "config.toml")

	// It used to be looked for where the system keeps settings, which on
	// macOS is another place: a file left there is still read.
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if dir, err := os.UserConfigDir(); err == nil {
			old := filepath.Join(dir, "cicoria", "config.toml")
			if _, err := os.Stat(old); err == nil {
				return old
			}
		}
	}
	return path
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
	if cfg.Changes != ChangesUncommitted && cfg.Changes != ChangesSession {
		return cfg, fmt.Errorf("%s: changes is %q: it can be %q or %q", path, cfg.Changes, ChangesUncommitted, ChangesSession)
	}
	for name, a := range cfg.Agents {
		if len(a.Command) == 0 {
			return cfg, fmt.Errorf("%s: agent %q has no command", path, name)
		}
	}
	return cfg, nil
}

// Adapters returns the agents to offer: Claude Code and Copilot CLI first,
// then the configured ones by name, then a plain shell.
func (c Config) Adapters() []agent.Adapter {
	// A nil command line is the agent's own.
	adapters := []agent.Adapter{
		agent.NewClaude(c.Agents["claude"].Command),
		agent.NewCopilot(c.Agents["copilot"].Command),
	}

	names := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		if name != "claude" && name != "copilot" && name != "shell" {
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
