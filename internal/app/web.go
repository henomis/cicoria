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
	"path/filepath"
	"slices"
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
		srv, err := web.Start(m.cfg.WebAddress)
		if err != nil {
			m.say("web cockpit: " + err.Error())
			return nil
		}
		m.web, m.webQR = srv, nil
		if qr, err := ui.QR(srv.URL(), m.dark); err == nil {
			m.webQR = qr
		}
		if s := m.current(); s != nil {
			s.Web = true
		}
		cmd = waitWeb(srv)
	}
	m.mode, m.webCursor = modeWeb, m.active
	m.publish()
	return cmd
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
			ID: s.ID, Name: s.Name, Agent: s.Adapter.Name(), Project: filepath.Base(s.Dir),
			Status: s.Status.String(), Since: s.Since, Stalled: s.Stalled(now),
			Queue: []string{}, Paused: s.Paused, Answer: s.Answer, Dir: s.Dir,
			Running: s.Term != nil && s.Status != agent.Exited,
		}
		if !s.Uncommitted {
			ws.Base = s.Base
		}
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

// handleWeb does what a page asked of a shared session.
func (m *Model) handleWeb(a web.Action) tea.Cmd {
	s, _ := m.byID(a.Session)
	if s == nil || !s.Web {
		return nil
	}
	defer m.publish()
	running := s.Term != nil && s.Status != agent.Exited

	switch a.Kind {
	case web.ActSend:
		if !running {
			return nil
		}
		m.say("from the web: a message for " + s.Name)
		return m.deliver(s, a.Text)
	case web.ActQueue:
		m.say("from the web: a message queued for " + s.Name)
		return m.enqueue(s, session.Message{Text: a.Text})
	case web.ActKey:
		if k, ok := webKeyPress(a.Text); ok && running {
			s.NoteInput(time.Now())
			s.Term.SendKey(k)
		}
	}
	return nil
}

// webKeyPress returns the key a page names: a printable character, or one
// of the few keys that answer an agent's questions.
func webKeyPress(name string) (uv.Key, bool) {
	switch name {
	case "enter":
		return uv.Key{Code: uv.KeyEnter}, true
	case "esc":
		return uv.Key{Code: uv.KeyEscape}, true
	case "up":
		return uv.Key{Code: uv.KeyUp}, true
	case "down":
		return uv.Key{Code: uv.KeyDown}, true
	case "left":
		return uv.Key{Code: uv.KeyLeft}, true
	case "right":
		return uv.Key{Code: uv.KeyRight}, true
	case "tab":
		return uv.Key{Code: uv.KeyTab}, true
	}
	if r := []rune(name); len(r) == 1 && unicode.IsPrint(r[0]) {
		return uv.Key{Code: r[0], Text: name}, true
	}
	return uv.Key{}, false
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
		if s.Web {
			check = ui.Info.Render("[x]")
		}
		rows = append(rows, fmt.Sprintf(" %s %s %s %s ", check, badge(s), s.Name, ui.Dim.Render(s.Status.String())))
	}
	hint := ui.Dim.Render(" space share ⇄ stop sharing · x stop the cockpit · esc close")

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
