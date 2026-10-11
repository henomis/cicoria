package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// workspace is a saved workspace other than the one on screen.
type workspace struct {
	dir      string
	sessions int
	// here names its sessions that work in the directory this one was
	// opened on.
	here []string
	// busy says another cicoria has it open.
	busy bool
}

// workspaces returns the other saved workspaces whose directory still
// exists, those with a session working here first.
func (m *Model) workspaces() []workspace {
	var here, others []workspace
	for _, st := range session.Workspaces() {
		if st.Dir == m.dir {
			continue
		}
		if info, err := os.Stat(st.Dir); err != nil || !info.IsDir() {
			continue
		}
		w := workspace{dir: st.Dir, sessions: len(st.Sessions), busy: session.InUse(st.Dir)}
		for _, s := range st.Sessions {
			if s.Dir != "" && filepath.Clean(s.Dir) == filepath.Clean(m.dir) {
				w.here = append(w.here, s.Name)
			}
		}
		if len(w.here) > 0 {
			here = append(here, w)
		} else {
			others = append(others, w)
		}
	}
	return append(here, others...)
}

// hintWorkspaces says where to look for sessions when none is saved for the
// directory cicoria was opened on.
func (m *Model) hintWorkspaces() {
	all := m.workspaces()
	if len(all) == 0 {
		return
	}
	key := m.cfg.Prefix + " w"
	if w := all[0]; len(w.here) > 0 {
		m.say(fmt.Sprintf("%s works here from the workspace %s — %s opens it", strings.Join(w.here, ", "), tilde(w.dir), key))
	} else if len(all) == 1 {
		m.say(fmt.Sprintf("no session is saved for this directory — %s opens the workspace %s", key, tilde(w.dir)))
	} else {
		m.say(fmt.Sprintf("no session is saved for this directory — %s opens one of %d other workspaces", key, len(all)))
	}
	// It is the answer to "where are my sessions?": leave it time to be read.
	m.messageAt = m.messageAt.Add(hintFor - messageFor)
}

// askWorkspace offers the other saved workspaces.
func (m *Model) askWorkspace() tea.Cmd {
	all := m.workspaces()
	if len(all) == 0 {
		m.say("no other workspace is saved")
		return nil
	}
	var items []ui.Item
	for _, w := range all {
		hint := fmt.Sprintf("%d session(s)", w.sessions)
		if len(w.here) > 0 {
			hint = strings.Join(w.here, ", ") + " works here"
		}
		if w.busy {
			hint += " · open elsewhere"
		}
		items = append(items, ui.Item{Label: tilde(w.dir), Hint: hint, Value: w})
	}
	m.openPalette("workspace › ", items)
	return nil
}

// askOpen closes this workspace to open another in its place.
func (m *Model) askOpen(w workspace) tea.Cmd {
	open := func() tea.Cmd {
		return m.askLeave("open "+tilde(w.dir)+"?", w.dir)
	}
	if !w.busy {
		return open()
	}
	// Two of them would run the same agents on the same conversations.
	m.confirm = &confirm{
		question: tilde(w.dir) + " is open in another cicoria, and its agents would run twice. open it here too?",
		yes:      open,
	}
	return nil
}
