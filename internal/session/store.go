package session

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Saved is what is remembered of a session between runs.
type Saved struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Agent string `json:"agent"`
	// Dir is the session's working directory. Sessions saved before it was
	// recorded have none, and work in the directory cicoria was opened on.
	Dir string `json:"dir,omitempty"`
	// Conversation is the agent's own name for the conversation to continue,
	// when it is not ID.
	Conversation string `json:"conversation,omitempty"`
	// Base is the commit the session's changes are counted from.
	Base string `json:"base,omitempty"`
	// Queue and Paused are the messages waiting to be sent and whether they
	// are held back.
	Queue  []string `json:"queue,omitempty"`
	Paused bool     `json:"paused,omitempty"`
}

// State is a saved workspace.
type State struct {
	Sessions []Saved `json:"sessions"`
	Active   int     `json:"active"`
}

// StatePath returns where the workspace opened on dir is saved.
func StatePath(dir string) string {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".local", "state")
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(root, "cicoria", fmt.Sprintf("%x", sum[:8]), "sessions.json")
}

// LoadState reads a saved workspace. A missing file gives an empty one.
func LoadState(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// SaveState writes a workspace, replacing the file in one step.
func SaveState(path string, st State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
