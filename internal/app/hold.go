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
	"slices"
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
// finish it, then wait.
type hold struct {
	since, end time.Time
	// paused are the sessions whose queue the hold paused.
	paused []string
	// web says the hold stopped the web cockpit, shared which sessions it
	// shared then.
	web    bool
	shared []string
}

// startHold starts a hold, and asks what is on the user's mind, to write it
// down: a note for them, kept until they clear it, never sent to an agent.
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
	m.hold, m.back = h, nil
	m.save()
	m.prompt = &prompt{
		input: ui.Input{Prompt: "on hold · what's in your head? (esc skips) "},
		done: func(text string) tea.Cmd {
			if text != "" {
				m.note = text
				m.save()
			}
			return nil
		},
	}
	return nil
}

// endHold ends the hold and shows what happened while the user was away,
// with what is left for them to resume above it. The queues that have
// messages stay paused, to be resumed one by one.
func (m *Model) endHold() tea.Cmd {
	h := m.hold
	m.hold = nil
	h.end = time.Now()
	h.paused = slices.DeleteFunc(h.paused, func(id string) bool {
		s, _ := m.byID(id)
		if s != nil && len(s.Queue) == 0 {
			s.Paused = false
		}
		return s == nil || !s.Paused
	})
	m.back = h
	m.awayFrom = h.since
	m.save()
	return m.openTimelineOn(nil)
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
	if m.note != "" {
		lines = append(lines, "you wrote: "+m.note, "")
	} else {
		lines = append(lines, "write down where you were, then step away.", "")
	}
	lines = append(lines, ui.Dim.Render(state), ui.Dim.Render("agents mid-turn finish their turn, then wait"), "", ui.Dim.Render("enter: back to work"))
	for k, l := range lines {
		lines[k] = strings.Repeat(" ", max((m.width-ansi.StringWidth(l))/2, 0)) + l
	}
	return ui.Block(lines, m.width, height)
}

// backLines are what is left for the user after a hold, above the
// timeline: the note, the queues still paused, the web cockpit to start
// again. They stay until each is acted on, or x clears them all.
func (m *Model) backLines() []string {
	var out []string
	if b := m.back; b != nil {
		var paused []string
		for _, id := range b.paused {
			if s, _ := m.byID(id); s != nil && s.Paused {
				paused = append(paused, fmt.Sprintf("%s (%d)", s.Name, len(s.Queue)))
			}
		}
		if len(paused) == 0 && !b.web {
			m.back = nil
			m.save()
		} else {
			out = append(out, fmt.Sprintf(" back after %s · %s → %s", span(b.end.Sub(b.since)), b.since.Format("15:04"), b.end.Format("15:04"))+ui.Dim.Render(" · x clears this block and the note"))
			if len(paused) > 0 {
				out = append(out, " paused: "+strings.Join(paused, " · ")+ui.Dim.Render(" · p resumes one, the selected first"))
			}
			if b.web {
				out = append(out, " web cockpit stopped"+ui.Dim.Render(" · "+m.cfg.Prefix+" v starts it, new link, same sessions shared"))
			}
		}
	}
	if m.note != "" {
		out = append(out, " you wrote: "+m.note+ui.Dim.Render(" · x clears it"))
	}
	return out
}

// resumeQueue resumes the paused queue of a session, and sends its next
// message if the agent is ready for it: the user's call, from the timeline.
func (m *Model) resumeQueue(id string) tea.Cmd {
	s, _ := m.byID(id)
	s.Paused = false
	m.save()
	m.say("resumed the queue of " + s.Name)
	return m.trySend(s)
}
