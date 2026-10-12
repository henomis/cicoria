package app

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/henomis/cicoria/internal/agent"
)

// wheelStep is how many rows one notch of the mouse wheel scrolls.
const wheelStep = 3

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
		if key == "y" || key == "Y" || key == "enter" {
			return c.yes()
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
		m.mode = modeWorkspace
		return nil
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
			m.openFile(row.Path)
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
		// The text starts under the header line.
		if isClick && mouse.Y >= 1 && mouse.Y <= m.viewHeight() {
			m.view.pager.SetCursor(m.view.pager.LineAt(mouse.Y-1, m.viewHeight()), m.viewHeight())
		}
		return nil
	case modePalette, modeHelp:
		return nil
	}

	l := m.layout()
	x, y := mouse.X, mouse.Y
	switch {
	case l.agent.contains(x, y):
		if isClick {
			m.focus = focusAgent
		}
		m.forwardMouse(msg, l.agent)

	case l.sessions.contains(x, y):
		if row := y - l.sessions.y - 1; isClick && row >= 0 && row < len(m.sessions) {
			m.switchTo(row)
			m.focus = focusSessions
		}

	case l.files.contains(x, y):
		if isClick {
			m.focus = focusFiles
			tree := m.tree()
			if row := tree.Top + y - l.files.y - 1; row >= 0 && row < len(tree.Rows()) && y > l.files.y {
				tree.Cursor = row
			}
		}
		m.tree().Move(wheel)

	case l.changes.contains(x, y):
		if isClick {
			m.focus = focusChanges
			if row := m.changeTop + y - l.changes.y - 1; row >= 0 && row < len(m.changes) && y > l.changes.y {
				m.changeCursor = row
			}
		}
		m.changeCursor = max(min(m.changeCursor+wheel, len(m.changes)-1), 0)

	case l.queue.contains(x, y):
		if s := m.current(); isClick && s != nil {
			m.focus = focusQueue
			if row := y - l.queue.y - 1; row >= 0 && row < len(s.Queue) {
				m.queueCursor = row
			}
		}
	}
	return nil
}

// forwardMouse passes a mouse event to the agent on screen, in the
// coordinates of its pane.
func (m *Model) forwardMouse(msg tea.MouseMsg, pane rect) {
	s := m.current()
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
