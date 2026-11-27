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

	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// hold is a break the user takes: every queue is paused, notifications wait
// and the web cockpit is stopped, so that nothing calls them back. Agents
// in the middle of a turn finish it, then wait.
type hold struct {
	since time.Time
	// paused are the sessions whose queue the hold paused.
	paused []string
	// web says the hold stopped the web cockpit, note that the user wrote
	// what was in their head on the backlog.
	web, note bool
}

// startHold starts a hold. Any key or click ends it.
func (m *Model) startHold() tea.Cmd {
	h := &hold{since: time.Now(), web: m.web != nil}
	for _, s := range m.sessions {
		if !s.Paused {
			s.Paused = true
			h.paused = append(h.paused, s.ID)
		}
	}
	m.stopWeb()
	m.hold = h
	m.save()
	// What is on the user's mind goes on the backlog, so it can wait. It is
	// a note for the user: started, the card sends the agent nothing.
	m.prompt = &prompt{
		input: ui.Input{Prompt: "on hold · what's in your head? (esc skips) "},
		done: func(text string) tea.Cmd {
			if text != "" {
				m.cards = append(m.cards, session.Card{ID: session.NewID(), Title: text, Dir: m.project()})
				h.note = true
				m.save()
			}
			return nil
		},
	}
	return nil
}

// endHold ends the hold and shows what happened while the user was away.
// The queues that have messages stay paused, to be resumed one by one.
func (m *Model) endHold() tea.Cmd {
	h := m.hold
	m.hold = nil
	still := 0
	for _, s := range m.sessions {
		if !slices.Contains(h.paused, s.ID) {
			continue
		}
		if len(s.Queue) == 0 {
			s.Paused = false
		} else {
			still++
		}
	}
	m.awayFrom = h.since
	m.save()
	var said []string
	if h.note {
		said = append(said, "your note is on the board: "+m.cfg.Prefix+" 0")
	}
	if still > 0 {
		said = append(said, fmt.Sprintf("%d queue(s) still paused: p in the queue panel resumes one", still))
	}
	if h.web {
		said = append(said, m.cfg.Prefix+" v starts the web cockpit again")
	}
	if len(said) > 0 {
		m.say(strings.Join(said, " · "))
	}
	return m.openTimelineOn(nil)
}

// holdStatus is what the status bar says while a hold is on.
func (m *Model) holdStatus() string {
	text := fmt.Sprintf("on hold since %s · %d queue(s) paused · agents mid-turn still finish", m.hold.since.Format("15:04"), len(m.hold.paused))
	if m.hold.web {
		text += " · web cockpit stopped"
	}
	return text
}
