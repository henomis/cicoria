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
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/ui"
)

// holdTarget is how long a break has to last to restore attention: shorter
// pauses ease fatigue, not focus.
const holdTarget = 15 * time.Minute

// hold is a break the user takes: every queue is paused, no desktop
// notification is sent, the web cockpit is stopped and the agents are out of
// sight, so that nothing calls them back. Agents in the middle of a turn
// finish it, then wait. Ending it puts everything back as it was.
type hold struct {
	since time.Time
	// paused are the sessions whose queue the hold paused.
	paused []string
	// web says the hold stopped the web cockpit, shared which sessions it
	// shared then.
	web    bool
	shared []string
	// note is what the user wrote down when the hold started, shown until
	// it ends: writing it down is what lets it go.
	note string
}

// startHold starts a hold, and asks what is on the user's mind, to write it
// down on the hold screen. It is never sent to an agent, nor kept.
func (m *Model) startHold() tea.Cmd {
	h := &hold{since: time.Now(), web: m.web != nil}
	for _, s := range m.sessions {
		if !s.Paused {
			s.Paused = true
			h.paused = append(h.paused, s.ID)
		}
		if s.Web {
			h.shared = append(h.shared, s.ID)
		}
	}
	m.stopWeb()
	m.hold = h
	m.save()
	m.prompt = &prompt{
		input: ui.Input{Prompt: "on hold · what's in your head? (esc skips) "},
		done: func(text string) tea.Cmd {
			h.note = text
			return nil
		},
	}
	return nil
}

// endHold ends the hold and puts back what it changed: the queues it paused
// resume, each sending its next message when its agent is ready, as it would
// have, and the web cockpit starts again with the same sessions shared.
func (m *Model) endHold() tea.Cmd {
	h := m.hold
	m.hold = nil
	m.awayFrom = h.since
	var cmds []tea.Cmd
	for _, id := range h.paused {
		if s, _ := m.byID(id); s != nil {
			s.Paused = false
			cmds = append(cmds, m.trySend(s))
		}
	}
	if h.web {
		cmds = append(cmds, m.startWeb())
		for _, id := range h.shared {
			if s, _ := m.byID(id); s != nil {
				s.Web = true
			}
		}
		m.publish()
	}
	m.save()
	return tea.Batch(cmds...)
}

// renderHold draws the hold over the whole screen but the status bar, in
// place of the agents.
func (m *Model) renderHold() []string {
	height := max(m.height-1, 2)
	gone := time.Since(m.hold.since).Truncate(time.Minute)
	took := span(gone) + " of " + span(holdTarget)
	if gone >= holdTarget {
		took = span(gone)
	}
	state := fmt.Sprintf("%d queue(s) paused · no notifications", len(m.hold.paused))
	if m.hold.web {
		state += " · web cockpit stopped"
	}
	lines := []string{"", "", ui.Bold.Render("on hold since " + m.hold.since.Format("15:04") + " · " + took), ""}
	if m.hold.note != "" {
		lines = append(lines, "you wrote: "+m.hold.note, "")
	} else {
		lines = append(lines, "write down where you were, then step away.", "")
	}
	lines = append(lines, ui.Dim.Render(state), ui.Dim.Render("agents mid-turn finish their turn, then wait"), "", ui.Dim.Render("enter: back to work, as you left it"))
	for k, l := range lines {
		lines[k] = strings.Repeat(" ", max((m.width-ansi.StringWidth(l))/2, 0)) + l
	}
	return ui.Block(lines, m.width, height)
}
