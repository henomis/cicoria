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
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/ui"
)

// oldWorktree is a worktree of the repository on screen, as the list of
// worktrees shows it.
type oldWorktree struct {
	path string
	// user is a session that works there, by name and id, and kept why it
	// must be kept. When both are "", it can go.
	user, userID, kept string
	// lost is the work that removing it anyway would lose, when that is
	// what keeps it.
	lost string
}

// worktreeAction is something to do with a worktree picked from the list.
type worktreeAction struct {
	run func(m *Model) tea.Cmd
}

// sessionIn is an agent picked to start a new session in dir.
type sessionIn struct {
	a   agent.Adapter
	dir string
}

// worktreesMsg brings the worktrees of the repository on screen, looked at
// away from the screen: telling what can go reads every file of each.
type worktreesMsg struct {
	list []oldWorktree
	err  error
}

// askWorktrees lists the worktrees of the repository on screen, the ones
// that can go first, to act on one.
func (m *Model) askWorktrees() tea.Cmd {
	dir := m.project()
	if _, ok := git.Root(dir); !ok {
		m.say("not a git repository")
		return nil
	}
	type at struct{ name, id, dir string }
	var sessions []at
	for _, s := range m.sessions {
		sessions = append(sessions, at{s.Name, s.ID, s.Dir})
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
					w.user, w.userID = s.name, s.id
					break
				}
			}
			if w.user == "" {
				w.kept = keepWorktree(path)
				// Removing it anyway does not help with files that cannot
				// be deleted.
				if lost := unsaved(path); lost == w.kept {
					w.lost = lost
				}
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
	m.openPalette("worktree › ", items)
}

// chooseWorktree offers what can be done with a worktree picked from the
// list: going to the session that works there, starting one there, as to
// have an agent commit what it holds, opening a shell there, and removing it.
func (m *Model) chooseWorktree(w oldWorktree) {
	var items []ui.Item
	add := func(label, hint string, run func(m *Model) tea.Cmd) {
		items = append(items, ui.Item{Label: label, Hint: hint, Value: worktreeAction{run}})
	}
	if w.user != "" {
		add("Go to "+w.user, "", func(m *Model) tea.Cmd {
			if _, i := m.byID(w.userID); i >= 0 {
				return m.switchTo(i)
			}
			m.say(w.user + " is closed")
			return nil
		})
	}
	add("New session there", "", func(m *Model) tea.Cmd { return m.askSessionIn(w.path, false) })
	add("Shell there", "", func(m *Model) tea.Cmd { return m.askSessionIn(w.path, true) })
	switch {
	case w.user != "":
	case w.kept == "":
		// Whether it can go is looked at again once the answer is yes.
		add("Remove", "", func(m *Model) tea.Cmd { m.offerRemoveWorktree(w.path, ""); return nil })
	case w.lost != "":
		add("Remove anyway", strings.TrimPrefix(w.lost, "it ")+", lost with it",
			func(m *Model) tea.Cmd { m.offerDiscardWorktree(w.path, w.lost); return nil })
	}
	m.openPalette(filepath.Base(w.path)+" › ", items)
}

// askSessionIn starts a new session in dir: a shell, or one of the agents,
// asking which.
func (m *Model) askSessionIn(dir string, shell bool) tea.Cmd {
	if _, err := os.Stat(dir); err != nil {
		m.say(tilde(dir) + " is gone")
		return nil
	}
	var agents []agent.Adapter
	for _, a := range m.adapters {
		if isShell(a) == shell {
			agents = append(agents, a)
		}
	}
	if len(agents) == 1 {
		return m.askWorktree(agents[0], dir)
	}
	var items []ui.Item
	for _, a := range agents {
		items = append(items, ui.Item{Label: a.Name(), Value: sessionIn{a, dir}})
	}
	m.openPalette("agent › ", items)
	return nil
}

// offerDiscardWorktree asks whether to remove the worktree at path with the
// work it holds, which lost says.
func (m *Model) offerDiscardWorktree(path, lost string) {
	if s := m.workingIn(path); s != nil {
		m.say(s.Name + " works in " + tilde(path) + ": close it first")
		return
	}
	m.confirm = &confirm{
		question: fmt.Sprintf("remove the worktree %s anyway? %s, and they go with it", tilde(path), lost),
		yes: func() tea.Cmd {
			cfg := m.cfg
			return func() tea.Msg {
				if kept := teardown(cfg, path); kept != "" {
					return worktreeMsg{path, kept}
				}
				return removedMsg{path, git.DiscardWorktree(path)}
			}
		},
	}
}
