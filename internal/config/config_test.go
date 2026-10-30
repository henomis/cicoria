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

package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/henomis/cicoria/internal/agent"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prefix != "ctrl+a" || cfg.DefaultAgent != "claude" || !cfg.Notify || !cfg.LeftPanel || !cfg.RightPanel || !cfg.Animations || !cfg.IgnoredFiles || !cfg.CloseOnExit || cfg.Changes != ChangesUncommitted {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadAndAdapters(t *testing.T) {
	cfg, err := Load(write(t, `
prefix = "ctrl+b"
notify = false
right_panel = false
ignored_files = false
close_on_exit = false

[agents.copilot]
command = ["copilot", "--banner"]

[agents.aider]
command = ["aider"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prefix != "ctrl+b" || cfg.Notify || !cfg.LeftPanel || cfg.RightPanel || cfg.IgnoredFiles || cfg.CloseOnExit || cfg.DefaultAgent != "claude" {
		t.Errorf("cfg = %+v", cfg)
	}

	var names []string
	for _, a := range cfg.Adapters() {
		names = append(names, a.Name())
	}
	want := []string{"claude", "copilot", "codex", "aider", "shell"}
	if len(names) != len(want) {
		t.Fatalf("adapters = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("adapters = %v, want %v", names, want)
		}
	}
}

func TestAdaptersCommands(t *testing.T) {
	cfg, err := Load(write(t, "[agents.copilot]\ncommand = [\"copilot\", \"--banner\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// An entry named after an agent cicoria knows changes its command line.
	adapters := cfg.Adapters()
	if got := adapters[0].Command(agent.LaunchOpts{}).Args[0]; got != "claude" {
		t.Errorf("claude runs %q", got)
	}
	if got := adapters[1].Command(agent.LaunchOpts{}).Args[:2]; got[0] != "copilot" || got[1] != "--banner" {
		t.Errorf("copilot runs %q", got)
	}
	if !adapters[1].ReportsStatus() {
		t.Error("the configured copilot does not report its status")
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":   `prefx = "ctrl+b"`,
		"empty command": "[agents.x]\ncommand = []",
		"bad syntax":    `prefix = `,
		"no repo":       "[[worktree]]\nsetup = [\"make\"]",
		"no setup":      "[[worktree]]\nrepo = \"/src/app\"",
		"no ports":      "ports_per_session = 0",
		"low ports":     "port_base = 80",
		"too many":      "port_base = 65530",
	} {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestLoadChanges(t *testing.T) {
	cfg, err := Load(write(t, `changes = "session"`))
	if err != nil || cfg.Changes != ChangesSession {
		t.Errorf("changes = %q, %v", cfg.Changes, err)
	}
	if _, err := Load(write(t, `changes = "all"`)); err == nil {
		t.Error("an unknown kind of changes was accepted")
	}
}

func TestLoadWebAddress(t *testing.T) {
	cfg, err := Load(write(t, `web_address = "0.0.0.0:7777"`))
	if err != nil || cfg.WebAddress != "0.0.0.0:7777" {
		t.Errorf("web_address = %q, %v", cfg.WebAddress, err)
	}
	for _, bad := range []string{"7777", "localhost", "0.0.0.0:http", "0.0.0.0:70000"} {
		if _, err := Load(write(t, `web_address = "`+bad+`"`)); err == nil {
			t.Errorf("web_address %q was accepted", bad)
		}
	}
}

func TestLoadPorts(t *testing.T) {
	if cfg, err := Load(write(t, "")); err != nil || cfg.PortBase != 20000 || cfg.PortsPerSession != 10 {
		t.Errorf("ports by default = %d, %d, %v", cfg.PortBase, cfg.PortsPerSession, err)
	}
	if cfg, err := Load(write(t, "port_base = 0")); err != nil || cfg.PortBase != 0 {
		t.Errorf("port_base 0 = %d, %v", cfg.PortBase, err)
	}
	if cfg, err := Load(write(t, "port_base = 65535\nports_per_session = 1")); err != nil || cfg.PortBase != 65535 {
		t.Errorf("the last port = %d, %v", cfg.PortBase, err)
	}
}

func TestSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := filepath.Join(home, "src", "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "app")
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(write(t, "[[worktree]]\nrepo = \"~/src/app\"\nsetup = [\"npm\", \"ci\"]\n\n[[worktree]]\nrepo = \"/elsewhere\"\nteardown = [\"make\", \"down\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, main := range []string{app, app + "/", link} {
		if got := cfg.Worktree(main).Setup; !slices.Equal(got, []string{"npm", "ci"}) {
			t.Errorf("setup of %s = %v", main, got)
		}
	}
	if got := cfg.Worktree("/elsewhere"); got.Setup != nil || !slices.Equal(got.Teardown, []string{"make", "down"}) {
		t.Errorf("worktree with a teardown alone = %+v", got)
	}
	if got := cfg.Worktree(filepath.Join(home, "src", "other")); got.Setup != nil || got.Teardown != nil {
		t.Errorf("worktree of another repository = %+v", got)
	}
}

func TestLoadWorktreeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(write(t, `worktree_dir = "~/worktrees/"`))
	if want := filepath.Join(home, "worktrees"); err != nil || cfg.WorktreeDir != want {
		t.Errorf("worktree_dir = %q, %v; want %q", cfg.WorktreeDir, err, want)
	}
	if cfg, err := Load(write(t, "")); err != nil || cfg.WorktreeDir != "" {
		t.Errorf("worktree_dir by default = %q, %v", cfg.WorktreeDir, err)
	}
	if _, err := Load(write(t, `worktree_dir = "worktrees"`)); err == nil {
		t.Error("a relative worktree_dir was accepted")
	}
}

func TestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := Path(), filepath.Join(home, ".config", "cicoria", "config.toml"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := Path(), filepath.Join(xdg, "cicoria", "config.toml"); got != want {
		t.Errorf("Path() with XDG_CONFIG_HOME = %q, want %q", got, want)
	}
}
