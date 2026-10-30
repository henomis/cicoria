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

package app

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/ui"
)

// oldWorktree is a worktree of the repository on screen, as the list of
// worktrees shows it.
type oldWorktree struct {
	path string
	// user is a session that works there, and kept why it must be kept.
	// When both are "", it can go.
	user, kept string
}

// worktreesMsg brings the worktrees of the repository on screen, looked at
// away from the screen: telling what can go reads every file of each.
type worktreesMsg struct {
	list []oldWorktree
	err  error
}

// askWorktrees lists the worktrees of the repository on screen, the ones
// that can go first, to remove them.
func (m *Model) askWorktrees() tea.Cmd {
	dir := m.project()
	if _, ok := git.Root(dir); !ok {
		m.say("not a git repository")
		return nil
	}
	type at struct{ name, dir string }
	var sessions []at
	for _, s := range m.sessions {
		sessions = append(sessions, at{s.Name, s.Dir})
	}
	m.say("looking at the worktrees…")
	return func() tea.Msg {
		paths, err := git.Worktrees(dir)
		if err != nil {
			return worktreesMsg{err: err}
		}
		var list []oldWorktree
		for _, path := range paths {
			w := oldWorktree{path: path}
			for _, s := range sessions {
				if rel, err := filepath.Rel(path, s.dir); err == nil && filepath.IsLocal(rel) {
					w.user = s.name
					break
				}
			}
			if w.user == "" {
				w.kept = keepWorktree(path)
			}
			list = append(list, w)
		}
		return worktreesMsg{list: list}
	}
}

// showWorktrees offers the worktrees that were looked at, unless something
// else was opened in the meantime.
func (m *Model) showWorktrees(msg worktreesMsg) {
	switch {
	case msg.err != nil:
		m.say(msg.err.Error())
		return
	case len(msg.list) == 0:
		m.say("this repository has no worktree")
		return
	case m.mode == modePalette || m.mode == modeHelp || m.prompt != nil || m.confirm != nil:
		return
	}
	m.message = ""
	rank := func(w oldWorktree) int {
		switch {
		case w.user != "":
			return 2
		case w.kept != "":
			return 1
		}
		return 0
	}
	slices.SortStableFunc(msg.list, func(a, b oldWorktree) int { return cmp.Compare(rank(a), rank(b)) })
	var items []ui.Item
	for _, w := range msg.list {
		hint := "can be removed"
		switch {
		case w.user != "":
			hint = w.user + " works there"
		case w.kept != "":
			hint = "kept: " + strings.TrimPrefix(w.kept, "it ")
		}
		// The name first: the paths of a repository's worktrees mostly
		// differ at the end.
		hint += " · " + tilde(filepath.Dir(w.path))
		items = append(items, ui.Item{Label: filepath.Base(w.path), Hint: hint, Value: w})
	}
	m.openPalette("remove worktree › ", items)
}

// chooseWorktree offers to remove a worktree picked from the list, or says
// why it cannot go.
func (m *Model) chooseWorktree(w oldWorktree) {
	switch {
	case w.user != "":
		m.say(w.user + " works in " + tilde(w.path) + ": close it first")
	case w.kept != "":
		m.say("worktree kept at " + tilde(w.path) + ": " + w.kept)
	default:
		// Whether it can go is looked at again once the answer is yes.
		m.offerRemoveWorktree(w.path, "")
	}
}
