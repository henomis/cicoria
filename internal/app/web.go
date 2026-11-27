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
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
	"github.com/henomis/cicoria/internal/web"
)

// webMsg carries what a page of the web cockpit asks, and the server it
// came through.
type webMsg struct {
	srv *web.Server
	web.Action
}

// waitWeb blocks until a page of the web cockpit asks for something.
func waitWeb(srv *web.Server) tea.Cmd {
	return func() tea.Msg {
		select {
		case a := <-srv.Actions():
			return webMsg{srv, a}
		case <-srv.Done():
			return nil
		}
	}
}

// askWeb opens the web cockpit, starting it when it is off. The first time
// it shares the session on screen, so that the page has something to show.
func (m *Model) askWeb() tea.Cmd {
	var cmd tea.Cmd
	if m.web == nil {
		if cmd = m.startWeb(); m.web == nil {
			return nil
		}
		if s := m.current(); s != nil {
			s.Web = true
		}
	}
	m.mode, m.webCursor = modeWeb, m.active
	m.publish()
	return cmd
}

// startWeb starts the web cockpit, sharing nothing yet.
func (m *Model) startWeb() tea.Cmd {
	srv, err := web.Start(m.cfg.WebAddress)
	if err != nil {
		m.say("web cockpit: " + err.Error())
		return nil
	}
	m.web, m.webQR = srv, nil
	if qr, err := ui.QR(srv.URL()); err == nil {
		m.webQR = qr
	}
	// The columns of the board need a look at the work of each session.
	return tea.Batch(waitWeb(srv), m.pollBoard())
}

// stopWeb stops the web cockpit; no session is shared any more.
func (m *Model) stopWeb() {
	if m.web == nil {
		return
	}
	_ = m.web.Close()
	m.web, m.webQR = nil, nil
	for _, s := range m.sessions {
		s.Web = false
	}
}

func (m *Model) webKey(key string) tea.Cmd {
	switch key {
	case "j", "down":
		m.webCursor = min(m.webCursor+1, len(m.sessions)-1)
	case "k", "up":
		m.webCursor = max(m.webCursor-1, 0)
	case "space", " ", "enter":
		if m.webCursor >= 0 && m.webCursor < len(m.sessions) {
			s := m.sessions[m.webCursor]
			s.Web = !s.Web
		}
	case "w":
		// Watching is a way of sharing: it shares a session that was not.
		if m.webCursor >= 0 && m.webCursor < len(m.sessions) {
			s := m.sessions[m.webCursor]
			if !s.Web {
				s.Web, s.WebWatch = true, true
			} else {
				s.WebWatch = !s.WebWatch
			}
		}
	case "x":
		m.stopWeb()
		m.mode = modeWorkspace
		m.say("stopped the web cockpit")
	case "esc", "q":
		m.mode = modeWorkspace
	}
	m.publish()
	return nil
}

// publish hands the web cockpit what it shows: the shared sessions, with
// the screen of those a page looks at.
// lanes names the columns of the board for the page.
var lanes = [session.Lanes]string{
	session.LaneNeedsYou: "needs", session.LaneWorking: "working",
	session.LaneReview: "review", session.LaneSettled: "settled",
}

func (m *Model) publish() {
	if m.web == nil {
		return
	}
	now, watching := time.Now(), m.web.Watching()
	snap := web.Snapshot{Workspace: tilde(m.dir), Sessions: []web.Session{}}
	for _, s := range m.sessions {
		if !s.Web {
			continue
		}
		ws := web.Session{
			ID: s.ID, Name: s.Name, Agent: s.Adapter.Name(), Project: filepath.Base(s.Dir), Path: tilde(s.Dir),
			Status: s.Status.String(), Since: s.Since, Stalled: s.Stalled(now),
			Queue: []string{}, Paused: s.Paused, Answer: s.Answer, Dir: s.Dir,
			Running: s.Term != nil && s.Status != agent.Exited,
			Control: !s.WebWatch, Touched: len(s.Touched),
		}
		if s.Worktree != "" {
			ws.Worktree = filepath.Base(s.Worktree)
		}
		w := m.board.info[s.ID]
		ws.Lane = lanes[s.Lane(m.changed(s))]
		ws.Branch, ws.Unseen, ws.Notes = w.branch, s.Unseen, len(s.Notes)
		if !isShell(s.Adapter) {
			ws.Changed, ws.Commits = w.files, w.commits
		}
		if s.Tool.Name != "" {
			tool := s.Tool
			ws.Tool = &tool
		}
		if s.Status == agent.NeedsInput {
			ws.Question = s.Question
			if s.Term != nil {
				if menu, ok := agent.ReadMenu(s.Term.String()); ok {
					ws.Prompt, ws.Choices = menu.Prompt, menu.Choices
				}
			}
		}
		if !s.Uncommitted {
			ws.Base = s.Base
		}
		ws.FromStart = ws.Base != ""
		for _, q := range s.Queue {
			ws.Queue = append(ws.Queue, q.Text)
		}
		if s.Term != nil && slices.Contains(watching, s.ID) {
			ws.Screen = plainScreen(s.Term.String())
		}
		snap.Sessions = append(snap.Sessions, ws)
	}
	m.web.Publish(snap)
}

// plainScreen drops the blanks at the end of each line and of the screen.
func plainScreen(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// handleWeb does what a page asked of a shared session, and tells the page
// what came of it.
func (m *Model) handleWeb(a web.Action) tea.Cmd {
	s, _ := m.byID(a.Session)
	if s == nil || !s.Web {
		a.Reply(errors.New("this session is not shared any more"))
		return nil
	}
	defer m.publish()
	running := s.Term != nil && s.Status != agent.Exited
	direct := a.Kind == web.ActSend || a.Kind == web.ActChoose || a.Kind == web.ActInterrupt
	switch {
	case direct && s.WebWatch:
		a.Reply(errors.New("this session is shared to watch: queue the message instead"))
		return nil
	case direct && !running:
		a.Reply(errors.New("the agent is not running: queue the message instead"))
		return nil
	case a.Kind == web.ActSend && s.Status == agent.NeedsInput:
		// Typed into a menu, a message could pick one of its choices.
		if _, menu := agent.ReadMenu(s.Term.String()); menu {
			a.Reply(errors.New("it is asking you to pick an answer: answer it first, or queue the message"))
			return nil
		}
	}

	switch a.Kind {
	case web.ActSend:
		m.say("from the web: a message for " + s.Name)
		a.Reply(nil)
		return m.deliver(s, a.Text, "", session.FromWeb)
	case web.ActQueue:
		m.say("from the web: a message queued for " + s.Name)
		a.Reply(nil)
		return m.enqueueFrom(s, session.Message{Text: a.Text}, session.FromWeb)
	case web.ActUnqueue:
		// The page names the message it saw: if the queue moved on since,
		// the same message is looked for elsewhere, and nothing else goes.
		i := a.Index
		if i < 0 || i >= len(s.Queue) || s.Queue[i].Text != a.Text {
			i = slices.IndexFunc(s.Queue, func(q session.Message) bool { return q.Text == a.Text })
		}
		if i < 0 {
			a.Reply(errors.New("this message is not queued any more: it may have been sent"))
			return nil
		}
		m.recordText(s, session.Event{K: session.KindUnqueue, Src: session.FromWeb}, s.Queue[i].Text)
		s.RemoveQueued(i)
		m.save()
		m.say("from the web: a queued message taken back from " + s.Name)
		a.Reply(nil)
	case web.ActChoose:
		a.Reply(m.chooseFromWeb(s, a.Index, a.Text))
	case web.ActInterrupt:
		s.NoteInput(time.Now())
		s.Term.SendKey(uv.Key{Code: uv.KeyEscape})
		m.record(s, session.Event{K: session.KindInterrupted, Src: session.FromWeb})
		m.say("from the web: " + s.Name + " interrupted")
		a.Reply(nil)
	}
	return nil
}

// chooseFromWeb answers the menu on the agent's screen with choice i, as the page
// saw it. The menu is read again first: if the choice is not the same one
// any more, nothing is pressed, so that an answer never goes to another
// question.
func (m *Model) chooseFromWeb(s *session.Session, i int, label string) error {
	menu, ok := agent.ReadMenu(s.Term.String())
	if !ok || s.Status != agent.NeedsInput {
		return errors.New("the question is not on the screen any more: look again")
	}
	if i < 0 || i >= len(menu.Choices) || menu.Choices[i] != label {
		return errors.New("the question changed: look again before answering")
	}
	s.NoteInput(time.Now())
	if menu.Selected < 0 {
		// A menu without a cursor takes the number of the choice.
		for _, r := range strconv.Itoa(i + 1) {
			s.Term.SendKey(uv.Key{Code: r, Text: string(r)})
		}
	} else {
		// The cursor is moved onto the choice, which is then taken.
		step := uv.Key{Code: uv.KeyDown}
		if i < menu.Selected {
			step = uv.Key{Code: uv.KeyUp}
		}
		for range max(i-menu.Selected, menu.Selected-i) {
			s.Term.SendKey(step)
		}
	}
	s.Term.SendKey(uv.Key{Code: uv.KeyEnter})
	m.answered[s.ID] = s.Since
	m.record(s, session.Event{K: session.KindAnswer, V: label, Src: session.FromWeb})
	m.say(fmt.Sprintf("from the web: %q chosen for %s", label, s.Name))
	return nil
}

// renderWeb draws the web cockpit box: the code to scan, the link, and the
// sessions with whether each is shared. The code is left out when there is
// no room for it.
func (m *Model) renderWeb(maxW, maxH int) []string {
	url := m.web.URL()
	var lines []string
	if m.web.Local() {
		lines = append(lines, ui.Warn.Render(" only this machine can open it: set web_address to reach it from a phone"))
	}
	lines = append(lines, " "+ui.Accent.Render(url), "")
	var rows []string
	for _, s := range m.sessions {
		check := "[ ]"
		switch {
		case s.Web && s.WebWatch:
			check = ui.Info.Render("[w]")
		case s.Web:
			check = ui.Info.Render("[x]")
		}
		rows = append(rows, fmt.Sprintf(" %s %s %s %s ", check, badge(s), s.Name, ui.Dim.Render(s.Status.String())))
	}
	hint := ui.Dim.Render(" space share ⇄ stop sharing · w watch only ⇄ control · x stop the cockpit · esc close")

	width := 0
	for _, l := range slices.Concat(lines, rows, []string{hint}) {
		width = max(width, ansi.StringWidth(l))
	}
	width = min(width, maxW-3)
	for i, row := range rows {
		lines = append(lines, ui.Selected(row, width, i == m.webCursor, true))
	}
	lines = append(lines, "", hint)
	qrW := 0
	if len(m.webQR) > 0 {
		qrW = ansi.StringWidth(m.webQR[0])
	}
	if qrW > 0 && qrW+2 <= maxW && len(m.webQR)+len(lines)+3 <= maxH {
		pad := strings.Repeat(" ", max((max(width, qrW)-qrW)/2, 0))
		var code []string
		for _, row := range m.webQR {
			code = append(code, pad+row)
		}
		lines = append(append(code, ""), lines...)
		width = max(width, qrW)
	}
	return ui.Box("Web cockpit", lines, min(width+3, maxW), min(len(lines)+2, maxH), true)
}
