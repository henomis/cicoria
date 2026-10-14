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
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// submitAfter is how long after pasting a message enter is pressed. Pressed
// at once, agents take it for part of the paste.
const submitAfter = 150 * time.Millisecond

// askQueue asks for a message to queue for the session on screen.
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
		return m.enqueue(s, session.Message{Text: text})
	}
	m.prompt = p
	return nil
}

// ask is the choice of a session to send a message to from another one,
// which gets the answer when reply is set.
type ask struct {
	from, to string
	reply    bool
}

// askHow is the choice between a message and a question.
type askHow ask

// askSession asks which other session to send a message to.
func (m *Model) askSession() tea.Cmd {
	from := m.current()
	if from == nil {
		m.say(errNoSession.Error())
		return nil
	}
	var items []ui.Item
	for _, s := range m.sessions {
		if s != from {
			items = append(items, ui.Item{Label: s.Name, Hint: s.Adapter.Name(), Value: ask{from: from.ID, to: s.ID}})
		}
	}
	if len(items) == 0 {
		m.say("there is no other session")
		return nil
	}
	m.openPalette("message for › ", items)
	return nil
}

// askReply asks whether the answer to the message should come back to the
// session it is sent from.
func (m *Model) askReply(a ask) tea.Cmd {
	to, _ := m.byID(a.to)
	from, _ := m.byID(a.from)
	if to == nil || from == nil {
		return nil
	}
	// Only an agent that says when its turn ends has an answer to take.
	if !to.Adapter.ReportsStatus() {
		return m.askMessage(a)
	}
	hint := "goes to " + from.Name + " when " + to.Name + " ends its turn"
	if !to.Adapter.ReportsAnswer() {
		hint = "not supported by " + to.Adapter.Name()
	}
	m.openPalette(fmt.Sprintf("message for %s › ", to.Name), []ui.Item{
		{Label: "Send a message", Value: askHow(a)},
		{Label: "Ask, and get the answer here", Hint: hint, Value: askHow{from: a.from, to: a.to, reply: true}},
	})
	return nil
}

// askMessage asks for the message and, when its answer comes back, for what
// the session that gets it is to do with it.
func (m *Model) askMessage(a ask) tea.Cmd {
	to, _ := m.byID(a.to)
	from, _ := m.byID(a.from)
	if to == nil || from == nil {
		return nil
	}
	// An agent that does not tell what it said has no answer to send back.
	if a.reply && !to.Adapter.ReportsAnswer() {
		m.say("asking is not supported by " + to.Adapter.Name() + ": it does not tell what it answered")
		return nil
	}
	// send queues the message, unless its session was closed in the meantime.
	send := func(msg session.Message) tea.Cmd {
		to, _ := m.byID(a.to)
		if to == nil {
			return nil
		}
		return m.enqueue(to, msg)
	}

	verb := "queue for"
	if a.reply {
		verb = "ask"
	}
	p := &prompt{input: ui.Input{Prompt: fmt.Sprintf("%s %s: ", verb, to.Name)}}
	p.done = func(text string) tea.Cmd {
		switch {
		case text == "":
			return nil
		case !a.reply:
			return send(session.Message{Text: text})
		}
		then := &prompt{input: ui.Input{Prompt: fmt.Sprintf("then tell %s (optional): ", from.Name)}}
		then.done = func(follow string) tea.Cmd {
			return send(session.Message{Text: text, ReplyTo: a.from, Then: follow})
		}
		m.prompt = then
		return nil
	}
	m.prompt = p
	return nil
}

// answer follows the question of another session through what its agent
// reports, and sends what it said at the end of the turn back to the session
// that asked.
func (m *Model) answer(s *session.Session, u agent.Update) tea.Cmd {
	q, answered := s.Settle(u)
	if q == nil {
		return nil
	}
	to, _ := m.byID(q.ReplyTo)
	switch {
	case to == nil:
		m.say(s.Name + " answered a session that is gone")
		return nil
	case !answered:
		m.say(s.Name + " did not finish its turn: no answer for " + to.Name)
		return nil
	case strings.TrimSpace(u.Answer) == "":
		m.say(s.Name + " ended its turn without an answer for " + to.Name)
		return nil
	}
	m.say(s.Name + " answered " + to.Name)
	return m.enqueue(to, session.Message{Text: session.Answer(s.Name, *q, u.Answer)})
}

// enqueue adds a message to a session's queue. An agent that is idle gets
// it right away.
func (m *Model) enqueue(s *session.Session, msg session.Message) tea.Cmd {
	s.Enqueue(msg)
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
	msg, _ := s.Dequeue()
	m.queueCursor = max(min(m.queueCursor, len(s.Queue)-1), 0)
	m.save()
	// The turn this starts is the answer to a question from another session.
	if msg.ReplyTo != "" {
		s.Ask(msg)
	}
	return m.deliver(s, msg.Text)
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
	case "x":
		s.RemoveQueued(i)
		m.queueCursor = max(min(i, len(s.Queue)-1), 0)
		m.save()
	case "p":
		s.Paused = !s.Paused
		m.save()
		return m.trySend(s)
	case "enter":
		return m.sendNext(s)
	case "m":
		return m.askQueue()
	case "e":
		switch {
		case i >= len(s.Queue):
		case strings.Contains(s.Queue[i].Text, "\n"):
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
	id, old := s.ID, s.Queue[i].Text
	p := &prompt{input: ui.Input{Prompt: "edit message: "}}
	p.input.Set(old)
	p.done = func(text string) tea.Cmd {
		// The queue may have moved on while the message was being edited.
		if s, _ := m.byID(id); s != nil && text != "" && i < len(s.Queue) && s.Queue[i].Text == old {
			s.Queue[i].Text = text
			m.save()
		}
		return nil
	}
	m.prompt = p
}

// queueTop is the first message the queue panel shows in height rows: the
// cursor is kept in sight.
func (m *Model) queueTop(s *session.Session, height int) int {
	cursor := max(min(m.queueCursor, len(s.Queue)-1), 0)
	return ui.Window(cursor, 0, height, len(s.Queue))
}

func (m *Model) renderQueue(r rect) []string {
	s := m.current()
	if s == nil {
		return ui.Block(nil, r.w, r.h)
	}
	focused := m.focus == focusQueue
	inner, height := r.w-2, r.h-2

	cursor := max(min(m.queueCursor, len(s.Queue)-1), 0)
	top := m.queueTop(s, height)
	var lines []string
	for i := top; i < min(top+height, len(s.Queue)); i++ {
		lead := "  "
		if i == 0 {
			lead = ui.Accent.Render("▸ ")
		}
		// Of a message of several lines, the first tells which one it is.
		text, more, _ := strings.Cut(s.Queue[i].Text, "\n")
		if more != "" {
			text += " …"
		}
		// A question says who gets the answer.
		if from, _ := m.byID(s.Queue[i].ReplyTo); from != nil {
			text = "from " + from.Name + ": " + text
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
