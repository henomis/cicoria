package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// submitAfter is how long after pasting a message enter is pressed. Pressed
// at once, agents take it for part of the paste.
const submitAfter = 150 * time.Millisecond

// askQueue asks for messages to queue for the session on screen, one after
// the other until an empty line or esc.
func (m *Model) askQueue() tea.Cmd {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	id := s.ID
	p := &prompt{input: ui.Input{Prompt: fmt.Sprintf("queue for %s: ", s.Name)}}
	p.done = func(text string) tea.Cmd {
		s, _ := m.byID(id)
		if s == nil || text == "" {
			return nil
		}
		cmd := m.enqueue(s, text)
		return tea.Batch(cmd, m.askQueue())
	}
	m.prompt = p
	return nil
}

// enqueue adds a message to a session's queue. An agent that is idle gets
// it right away.
func (m *Model) enqueue(s *session.Session, text string) tea.Cmd {
	s.Enqueue(text)
	m.save()
	return m.trySend(s)
}

// trySend sends the next queued message if the agent is ready for it.
func (m *Model) trySend(s *session.Session) tea.Cmd {
	if !s.CanAutoSend() {
		return nil
	}
	return m.sendNext(s)
}

// sendNext hands the first queued message to the agent: pasted now, with
// enter following shortly.
func (m *Model) sendNext(s *session.Session) tea.Cmd {
	if !s.CanSend() {
		return nil
	}
	text, _ := s.Dequeue()
	m.queueCursor = max(min(m.queueCursor, len(s.Queue)-1), 0)
	m.save()
	return m.deliver(s, text)
}

// deliver gives a message to a running agent: pasted now, with enter
// following shortly.
func (m *Model) deliver(s *session.Session, text string) tea.Cmd {
	now := time.Now()
	s.NoteInput(now)
	s.Term.Paste(text)
	// From here the agent counts as working, so that nothing else is sent
	// before it says so itself.
	s.Sent(now)

	id, t := s.ID, s.Term
	return tea.Tick(submitAfter, func(time.Time) tea.Msg { return submitMsg{id, t} })
}

func (m *Model) queueKey(key string) tea.Cmd {
	if m.panelKey(key) {
		return nil
	}
	s := m.current()
	if s == nil {
		return nil
	}
	m.queueCursor = max(min(m.queueCursor, len(s.Queue)-1), 0)
	i := m.queueCursor

	switch key {
	case "j", "down":
		m.queueCursor = min(i+1, len(s.Queue)-1)
	case "k", "up":
		m.queueCursor = max(i-1, 0)
	case "J":
		m.queueCursor = s.MoveQueued(i, 1)
		m.save()
	case "K":
		m.queueCursor = s.MoveQueued(i, -1)
		m.save()
	case "x", "d":
		s.RemoveQueued(i)
		m.queueCursor = max(min(i, len(s.Queue)-1), 0)
		m.save()
	case "p":
		s.Paused = !s.Paused
		m.save()
		return m.trySend(s)
	case "enter":
		return m.sendNext(s)
	case "a":
		return m.askQueue()
	case "e":
		switch {
		case i >= len(s.Queue):
		case strings.Contains(s.Queue[i], "\n"):
			m.say("a message of several lines cannot be changed here")
		default:
			m.askEdit(s, i)
		}
	}
	return nil
}

// askEdit lets the user change a queued message. Emptying it changes
// nothing; deleting has its own key.
func (m *Model) askEdit(s *session.Session, i int) {
	id, old := s.ID, s.Queue[i]
	p := &prompt{input: ui.Input{Prompt: "edit message: "}}
	p.input.Set(old)
	p.done = func(text string) tea.Cmd {
		// The queue may have moved on while the message was being edited.
		if s, _ := m.byID(id); s != nil && text != "" && i < len(s.Queue) && s.Queue[i] == old {
			s.Queue[i] = text
			m.save()
		}
		return nil
	}
	m.prompt = p
}

func (m *Model) renderQueue(r rect) []string {
	s := m.current()
	if s == nil {
		return ui.Block(nil, r.w, r.h)
	}
	focused := m.focus == focusQueue
	inner, height := r.w-2, r.h-2

	cursor := max(min(m.queueCursor, len(s.Queue)-1), 0)
	top := ui.Window(cursor, 0, height, len(s.Queue))
	var lines []string
	for i := top; i < min(top+height, len(s.Queue)); i++ {
		lead := "  "
		if i == 0 {
			lead = ui.Accent.Render("▸ ")
		}
		// Of a message of several lines, the first tells which one it is.
		text, more, _ := strings.Cut(s.Queue[i], "\n")
		if more != "" {
			text += " …"
		}
		lines = append(lines, ui.Selected(lead+text, inner, focused && i == cursor, true))
	}

	title := fmt.Sprintf("Queue (%d)", len(s.Queue))
	switch {
	case s.Paused:
		title += " · paused"
	case !s.Adapter.ReportsStatus():
		title += " · manual"
	}
	return ui.Box(title, lines, r.w, r.h, focused)
}
