package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if cfg.Prefix != "ctrl+a" || cfg.DefaultAgent != "claude" || !cfg.Notify || !cfg.LeftPanel || !cfg.RightPanel {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadAndAdapters(t *testing.T) {
	cfg, err := Load(write(t, `
prefix = "ctrl+b"
notify = false
right_panel = false

[agents.copilot]
command = ["copilot", "--banner"]

[agents.aider]
command = ["aider"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Prefix != "ctrl+b" || cfg.Notify || !cfg.LeftPanel || cfg.RightPanel || cfg.DefaultAgent != "claude" {
		t.Errorf("cfg = %+v", cfg)
	}

	var names []string
	for _, a := range cfg.Adapters() {
		names = append(names, a.Name())
	}
	want := []string{"claude", "aider", "copilot", "shell"}
	if len(names) != len(want) {
		t.Fatalf("adapters = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("adapters = %v, want %v", names, want)
		}
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":   `prefx = "ctrl+b"`,
		"empty command": "[agents.x]\ncommand = []",
		"bad syntax":    `prefix = `,
	} {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}
