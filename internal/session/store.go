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

package session

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
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
	// Worktree is the git worktree that was made for the session, if any.
	Worktree string `json:"worktree,omitempty"`
	// Notes are the remarks on code not yet sent to the agent.
	Notes []Note `json:"notes,omitempty"`
	// Queue and Paused are the messages waiting to be sent and whether they
	// are held back.
	Queue  []Message `json:"queue,omitempty"`
	Paused bool      `json:"paused,omitempty"`
}

// State is a saved workspace.
type State struct {
	// Dir is the directory the workspace was opened on. Workspaces saved
	// before it was recorded have none, and are not listed.
	Dir      string  `json:"dir,omitempty"`
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

// Workspaces returns every saved workspace that says which directory it
// belongs to and has sessions, ordered by directory.
func Workspaces() []State {
	// Every workspace is filed next to this one.
	root := filepath.Dir(filepath.Dir(StatePath("")))
	paths, _ := filepath.Glob(filepath.Join(root, "*", "sessions.json"))
	var all []State
	for _, path := range paths {
		st, err := LoadState(path)
		if err == nil && st.Dir != "" && len(st.Sessions) > 0 {
			all = append(all, st)
		}
	}
	slices.SortFunc(all, func(a, b State) int { return cmp.Compare(a.Dir, b.Dir) })
	return all
}

// lockPath returns the file that is locked while the workspace opened on
// dir is in use.
func lockPath(dir string) string {
	return filepath.Join(filepath.Dir(StatePath(dir)), "lock")
}

// Lock marks the workspace opened on dir as in use, until the returned
// function is called or the process ends. It does nothing when the workspace
// is already in use: opening one twice is allowed, and asked about first.
func Lock(dir string) (release func()) {
	path := lockPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() { _ = f.Close() }
}

// InUse reports whether the workspace opened on dir is marked as in use.
func InUse(dir string) bool {
	f, err := os.Open(lockPath(dir))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
}
