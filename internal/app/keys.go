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
	"math"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
)

// wheelStep is how many rows one notch of the mouse wheel scrolls.
const wheelStep = 3

// wheelSlide is how many cells one notch sideways scrolls a line that is cut.
const wheelSlide = 8

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()

	// Any key ends an animation. When something waits for its end, the key
	// only brings that forward.
	if then := m.stopAnim(); then != nil {
		return then
	}

	// A pending question or text field takes every key.
	if c := m.confirm; c != nil {
		m.confirm = nil
		if key == "y" || key == "Y" {
			return c.yes()
		}
		// Enter takes the answer the question offers, which is no.
		if c.no != nil && (c.always || key == "n" || key == "N" || key == "enter") {
			return c.no()
		}
		return nil
	}
	if p := m.prompt; p != nil {
		switch key {
		case "enter":
			m.prompt = nil
			return p.done(strings.TrimSpace(p.input.Value()))
		case "esc":
			m.prompt = nil
		case "tab":
			if p.complete != nil {
				p.input.Set(p.complete(p.input.Value()))
			}
		default:
			p.input.Key(k)
		}
		return nil
	}

	switch m.mode {
	case modeHelp:
		// The keys that scroll elsewhere scroll help; any other closes it.
		page := max(m.height-4, 1)
		switch key {
		case "j", "down":
			m.helpTop++
		case "k", "up":
			m.helpTop = max(m.helpTop-1, 0)
		case "space", " ", "f", "pgdown":
			m.helpTop += page
		case "b", "pgup":
			m.helpTop = max(m.helpTop-page, 0)
		case "g", "home":
			m.helpTop = 0
		case "G", "end":
			m.helpTop = math.MaxInt32
		default:
			m.mode = m.helpFrom
		}
		return nil
	case modeWeb:
		return m.webKey(key)
	case modePalette:
		if key == "esc" {
			m.mode, m.palette = m.paletteFrom, nil
			return nil
		}
		if it, ok := m.palette.Key(k); ok {
			return m.choose(it)
		}
		return nil
	case modeView:
		// The prefix works here too, except while a search is typed.
		if m.view.search == nil {
			if m.prefix {
				m.prefix = false
				return m.runPrefix(k)
			}
			if key == m.cfg.Prefix {
				m.prefix = true
				return nil
			}
		}
		return m.viewKey(k)
	}

	if m.prefix {
		m.prefix = false
		return m.runPrefix(k)
	}
	if key == m.cfg.Prefix {
		m.prefix = true
		return nil
	}

	switch m.focus {
	case focusSessions:
		return m.sessionsKey(key)
	case focusFiles:
		return m.filesKey(key)
	case focusChanges:
		return m.changesKey(key)
	case focusCommits:
		return m.logKey(key)
	case focusQueue:
		return m.queueKey(key)
	}
	m.send(k)
	return nil
}

// panelKey handles the keys every side panel shares. It reports whether the
// key was one of them.
func (m *Model) panelKey(key string) bool {
	switch key {
	case "esc", "q":
		m.focus = focusAgent
	case "tab":
		// Only the panels on screen take part in the round.
		l := m.layout()
		var round []focus
		if l.left {
			round = append(round, focusSessions, focusFiles)
		}
		if l.right {
			round = append(round, focusChanges)
			if l.log.h > 0 {
				round = append(round, focusCommits)
			}
			if l.queue.h > 0 {
				round = append(round, focusQueue)
			}
		}
		if i := slices.Index(round, m.focus); i >= 0 {
			m.focus = round[(i+1)%len(round)]
		}
	default:
		return false
	}
	return true
}

func (m *Model) sessionsKey(key string) tea.Cmd {
	if m.panelKey(key) {
		return nil
	}
	switch key {
	case "j", "down":
		m.sessionCursor = max(min(m.sessionCursor+1, len(m.sessions)-1), 0)
	case "k", "up":
		m.sessionCursor = max(m.sessionCursor-1, 0)
	case "enter", "l", "right":
		return m.switchTo(m.sessionCursor)
	case ",":
		return m.askRename()
	case "r":
		return m.restart()
	case "x":
		return m.askClose()
	case "c":
		return m.askNewSession()
	}
	return nil
}

func (m *Model) filesKey(key string) tea.Cmd {
	if m.panelKey(key) {
		return nil
	}
	row := m.tree().Selected()
	switch key {
	case "j", "down":
		m.tree().Move(1)
	case "k", "up":
		m.tree().Move(-1)
	case "pgdown":
		m.tree().Move(m.layout().files.h - 2)
	case "pgup":
		m.tree().Move(-(m.layout().files.h - 2))
	case "h", "left":
		m.tree().SetOpen(false)
	case "enter", "l", "right":
		if row.Dir {
			m.tree().SetOpen(key != "enter" || !row.Open)
			if row.Ignored {
				return m.refreshNow()
			}
		} else if row.Path != "" {
			m.showFile(row.Path)
		}
	case "d":
		if c, ok := m.change(row.Path); ok {
			m.openDiff(c)
		} else if row.Path != "" {
			m.say(row.Path + " has no changes")
		}
	case "e":
		if !row.Dir {
			return m.edit(row.Path)
		}
	case "o":
		return m.open(row.Path)
	case "@":
		m.mention(row.Path)
	case "i":
		m.showIgnored = !m.showIgnored
		if m.showIgnored {
			m.say("ignored files shown")
		} else {
			m.say("ignored files hidden")
		}
		return m.refreshNow()
	}
	return nil
}

func (m *Model) changesKey(key string) tea.Cmd {
	if m.panelKey(key) {
		return nil
	}
	var path string
	if m.changeCursor < len(m.changes) {
		path = m.changes[m.changeCursor].Path
	}
	switch key {
	case "j", "down":
		m.changeCursor = max(min(m.changeCursor+1, len(m.changes)-1), 0)
	case "k", "up":
		m.changeCursor = max(m.changeCursor-1, 0)
	case "enter", "l", "right", "d":
		if path != "" {
			m.openDiff(m.changes[m.changeCursor])
		}
	case "v":
		if path != "" {
			m.openFile(path)
		}
	case "e":
		return m.edit(path)
	case "o":
		return m.open(path)
	case "@":
		m.mention(path)
	case "s":
		// Everything since the session started, or what is not committed.
		if s := m.current(); s != nil && s.Base != "" {
			s.Uncommitted = !s.Uncommitted
			m.commit = ""
			if s.Uncommitted {
				m.say("changes of " + s.Name + ": what is not committed")
			} else {
				m.say("changes of " + s.Name + ": everything since it started")
			}
			return m.refresh()
		}
		m.say("this session has no commit to count its changes from")
	}
	return nil
}

// logKey handles the keys of the commits panel.
func (m *Model) logKey(key string) tea.Cmd {
	if m.panelKey(key) {
		return nil
	}
	var c git.Commit
	if m.logCursor < len(m.log) {
		c = m.log[m.logCursor]
	}
	switch key {
	case "j", "down":
		m.logCursor = max(min(m.logCursor+1, len(m.log)-1), 0)
	case "k", "up":
		m.logCursor = max(m.logCursor-1, 0)
	case "enter", "l", "right":
		if c.Hash == "" {
			return nil
		}
		// The commit already narrowed to gives all the changes back.
		if c.Hash == m.commit {
			m.say("all the changes since the session started")
			return m.narrow("")
		}
		m.focus = focusChanges
		m.say("changes of " + c.Short + ": " + c.Subject)
		return m.narrow(c.Hash)
	case "d":
		if c.Hash != "" {
			m.openCommit(c)
		}
	case "@":
		if s := m.current(); c.Hash != "" && s != nil && s.Term != nil && s.Status != agent.Exited {
			s.Term.Type(c.Short + " ")
			m.focus = focusAgent
		}
	}
	return nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	mouse := msg.Mouse()
	_, isWheel := msg.(tea.MouseWheelMsg)
	_, isClick := msg.(tea.MouseClickMsg)

	wheel := 0
	if isWheel {
		switch mouse.Button {
		case tea.MouseWheelUp:
			wheel = -wheelStep
		case tea.MouseWheelDown:
			wheel = wheelStep
		}
	}

	switch m.mode {
	case modeView:
		m.view.pager.Scroll(wheel, m.viewHeight())
		switch {
		case isWheel && mouse.Button == tea.MouseWheelRight:
			m.view.pager.Slide(wheelSlide)
		case isWheel && mouse.Button == tea.MouseWheelLeft:
			m.view.pager.Slide(-wheelSlide)
		}
		// The text starts under the header line.
		if isClick && mouse.Y >= 1 && mouse.Y <= m.viewHeight() {
			m.view.pager.SetCursor(m.view.pager.LineAt(mouse.Y-1, m.viewHeight()), m.viewHeight())
		}
		return nil
	case modeHelp:
		m.helpTop = max(m.helpTop+wheel, 0)
		return nil
	case modePalette, modeWeb:
		return nil
	}

	l := m.layout()
	x, y := mouse.X, mouse.Y
	switch {
	case l.agent.contains(x, y):
		if isClick {
			m.focus = focusAgent
		}
		m.forwardMouse(msg, m.current(), l.agent)

	case l.other.contains(x, y):
		// A click takes the keys there; the wheel scrolls without them.
		if isClick {
			return m.focusOther()
		}
		if isWheel {
			m.forwardMouse(msg, m.other(), l.other)
		}

	case l.sessions.contains(x, y):
		if row := m.sessionTop + y - l.sessions.y - 1; isClick && row >= 0 && row < len(m.sessions) && y > l.sessions.y && y < l.sessions.y+l.sessions.h-1 {
			cmd := m.switchTo(row)
			m.focus = focusSessions
			return cmd
		}

	case l.files.contains(x, y):
		if isClick {
			m.focus = focusFiles
			tree := m.tree()
			if row := tree.Top + y - l.files.y - 1; row >= 0 && row < len(tree.Rows()) && y > l.files.y && y < l.files.y+l.files.h-1 {
				tree.Cursor = row
			}
		}
		m.tree().Move(wheel)

	case l.changes.contains(x, y):
		if isClick {
			m.focus = focusChanges
			if row := m.changeTop + y - l.changes.y - 1; row >= 0 && row < len(m.changes) && y > l.changes.y && y < l.changes.y+l.changes.h-1 {
				m.changeCursor = row
			}
		}
		m.changeCursor = max(min(m.changeCursor+wheel, len(m.changes)-1), 0)

	case l.log.contains(x, y):
		if isClick {
			m.focus = focusCommits
			if row := m.logTop + y - l.log.y - 1; row >= 0 && row < len(m.log) && y > l.log.y && y < l.log.y+l.log.h-1 {
				m.logCursor = row
			}
		}
		m.logCursor = max(min(m.logCursor+wheel, len(m.log)-1), 0)

	case l.queue.contains(x, y):
		if s := m.current(); isClick && s != nil {
			m.focus = focusQueue
			if row := m.queueTop(s, l.queue.h-2) + y - l.queue.y - 1; row >= 0 && row < len(s.Queue) && y > l.queue.y && y < l.queue.y+l.queue.h-1 {
				m.queueCursor = row
			}
		}
		if s := m.current(); s != nil {
			m.queueCursor = max(min(m.queueCursor+wheel, len(s.Queue)-1), 0)
		}
	}
	return nil
}

// forwardMouse passes a mouse event to the agent of a session on screen, in
// the coordinates of its pane.
func (m *Model) forwardMouse(msg tea.MouseMsg, s *session.Session, pane rect) {
	if s == nil || s.Term == nil || s.Status == agent.Exited {
		return
	}
	// An agent that does not read the mouse has its screen scrolled for it.
	if _, isWheel := msg.(tea.MouseWheelMsg); isWheel && !s.Term.OwnsScrolling() {
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			s.Term.Scroll(wheelStep)
		case tea.MouseWheelDown:
			s.Term.Scroll(-wheelStep)
		}
		return
	}
	mouse := uv.Mouse(msg.Mouse())
	mouse.X -= pane.x
	mouse.Y -= pane.y

	switch msg.(type) {
	case tea.MouseClickMsg:
		s.NoteInput(time.Now())
		s.Term.SendMouse(uv.MouseClickEvent(mouse))
	case tea.MouseReleaseMsg:
		s.Term.SendMouse(uv.MouseReleaseEvent(mouse))
	case tea.MouseMotionMsg:
		s.Term.SendMouse(uv.MouseMotionEvent(mouse))
	case tea.MouseWheelMsg:
		s.Term.SendMouse(uv.MouseWheelEvent(mouse))
	}
}
