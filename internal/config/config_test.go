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
	cfg, err := Load(write(t, "[[worktree]]\nrepo = \"~/src/app\"\nsetup = [\"npm\", \"ci\"]\n\n[[worktree]]\nrepo = \"/elsewhere\"\nsetup = [\"make\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, main := range []string{app, app + "/", link} {
		if got := cfg.Setup(main); !slices.Equal(got, []string{"npm", "ci"}) {
			t.Errorf("setup of %s = %v", main, got)
		}
	}
	if got := cfg.Setup(filepath.Join(home, "src", "other")); got != nil {
		t.Errorf("setup of another repository = %v", got)
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
