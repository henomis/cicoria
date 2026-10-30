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
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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
	// WebAddress is the address the web cockpit listens on, host and port.
	// Port 0 picks a free one.
	WebAddress string `toml:"web_address"`
	// PortBase is the first of the ports given to sessions, PortsPerSession
	// to each: a session has its own range, whose first port it is told in
	// CICORIA_PORT. A PortBase of 0 gives sessions no ports.
	PortBase        int `toml:"port_base"`
	PortsPerSession int `toml:"ports_per_session"`
	// Agents are command lines to offer as agents, by name. An entry named
	// "claude", "copilot" or "codex" changes how Claude Code, Copilot CLI
	// or Codex CLI is started.
	Agents map[string]AgentConfig `toml:"agents"`
	// WorktreeDir is where the worktrees made for sessions go, each in a
	// directory named after its repository. "" puts them next to the
	// repository, in <repository>.worktrees. Load makes it absolute.
	WorktreeDir string `toml:"worktree_dir"`
	// Worktrees say what to do in a worktree made for a session, by the
	// repository it is made from.
	Worktrees []WorktreeConfig `toml:"worktree"`
}

// WorktreeConfig is what to do in the worktrees made from one repository.
type WorktreeConfig struct {
	// Repo is the main work tree of the repository. ~ stands for the home
	// directory.
	Repo string `toml:"repo"`
	// Setup is the command line run in each new worktree, as dependencies
	// are installed.
	Setup []string `toml:"setup"`
	// Teardown is the command line run in a worktree before it is removed,
	// as containers started there are stopped.
	Teardown []string `toml:"teardown"`
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
	return Config{Prefix: "ctrl+a", DefaultAgent: "claude", Notify: true, LeftPanel: true, RightPanel: true, Changes: ChangesUncommitted, IgnoredFiles: true, CloseOnExit: true, Animations: true, WebAddress: "0.0.0.0:0", PortBase: 20000, PortsPerSession: 10}
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
	if _, port, err := net.SplitHostPort(cfg.WebAddress); err != nil {
		return cfg, fmt.Errorf("%s: web_address is %q: it is a host and a port, like %q", path, cfg.WebAddress, "0.0.0.0:7777")
	} else if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return cfg, fmt.Errorf("%s: web_address is %q: the port is a number up to 65535", path, cfg.WebAddress)
	}
	for name, a := range cfg.Agents {
		if len(a.Command) == 0 {
			return cfg, fmt.Errorf("%s: agent %q has no command", path, name)
		}
	}
	if cfg.PortsPerSession < 1 {
		return cfg, fmt.Errorf("%s: ports_per_session is %d: it is at least 1", path, cfg.PortsPerSession)
	}
	if cfg.PortBase != 0 && (cfg.PortBase < 1024 || cfg.PortBase+cfg.PortsPerSession-1 > 65535) {
		return cfg, fmt.Errorf("%s: port_base is %d: it is 0, or a port from 1024 that leaves room for ports_per_session", path, cfg.PortBase)
	}
	if cfg.WorktreeDir != "" {
		cfg.WorktreeDir = expandHome(cfg.WorktreeDir)
		if !filepath.IsAbs(cfg.WorktreeDir) {
			return cfg, fmt.Errorf("%s: worktree_dir is %q: it is an absolute path, or one that starts with ~", path, cfg.WorktreeDir)
		}
		cfg.WorktreeDir = filepath.Clean(cfg.WorktreeDir)
	}
	for _, w := range cfg.Worktrees {
		if w.Repo == "" {
			return cfg, fmt.Errorf("%s: a worktree has no repo", path)
		}
		if len(w.Setup) == 0 && len(w.Teardown) == 0 {
			return cfg, fmt.Errorf("%s: the worktree of %q has no setup and no teardown", path, w.Repo)
		}
	}
	return cfg, nil
}

// Worktree returns what to do in the worktrees of the repository whose
// main work tree is main: nothing when the settings do not name it.
func (c Config) Worktree(main string) WorktreeConfig {
	for _, w := range c.Worktrees {
		if samePath(expandHome(w.Repo), main) {
			return w
		}
	}
	return WorktreeConfig{}
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok || rest != "" && rest[0] != '/' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}

// samePath reports whether a and b name one directory, through symbolic
// links too.
func samePath(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// Shell is the name of the plain shell, which is always offered.
const Shell = "shell"

// Adapters returns the agents to offer: Claude Code, Copilot CLI and Codex
// CLI first, then the configured ones by name, then a plain shell.
func (c Config) Adapters() []agent.Adapter {
	// A nil command line is the agent's own.
	adapters := []agent.Adapter{
		agent.NewClaude(c.Agents["claude"].Command),
		agent.NewCopilot(c.Agents["copilot"].Command),
		agent.NewCodex(c.Agents["codex"].Command),
	}

	names := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		if name != "claude" && name != "copilot" && name != "codex" && name != Shell {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		adapters = append(adapters, agent.NewGeneric(name, c.Agents[name].Command))
	}

	shell := []string{os.Getenv("SHELL")}
	if a, ok := c.Agents[Shell]; ok {
		shell = a.Command
	} else if shell[0] == "" {
		shell[0] = "sh"
	}
	return append(adapters, agent.NewGeneric(Shell, shell))
}
