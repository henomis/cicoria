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
	"slices"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
)

const (
	// historyLoad is how far back the history is read when cicoria opens,
	// and historyBytes how much of the file at most.
	historyLoad  = 7 * 24 * time.Hour
	historyBytes = 2 << 20
	// historyKeep is the most the file is let grow to.
	historyKeep = 5 << 20
	// awayAfter is how long without a key means the user was away.
	awayAfter = 10 * time.Minute
	// fromQueueWithin is how soon after cicoria sent a message a turn that
	// starts is taken to be for it, and not for one the user typed.
	fromQueueWithin = 10 * time.Second
)

// Where a message the agent gets comes from, besides being typed into the
// queue: what the history says of it.
const (
	fromNotes  = "notes"
	fromAsk    = "ask"
	fromAnswer = "answer"
	fromCard   = "card"
)

// openHistory reads the history of the workspace, after making room in it
// when no other cicoria has it open.
func (m *Model) openHistory(alone bool) {
	path := session.EventsPath(m.dir)
	now := time.Now()
	if alone {
		keep := time.Duration(m.cfg.Timeline.KeepDays) * 24 * time.Hour
		_ = session.CompactLedger(path, now.Add(-keep), historyKeep)
	}
	l, err := session.OpenLedger(path, now.Add(-historyLoad), historyBytes)
	if err != nil {
		m.say("could not read the history: " + err.Error())
	}
	m.history = l
	// The user was away since the last thing that happened.
	if events := l.Events(); len(events) > 0 {
		m.awayFrom = events[len(events)-1].T
	}
	m.history.Record(session.Event{T: now, K: session.KindRunStart})
}

// track has a session tell the history when its status changes.
func (m *Model) track(s *session.Session) {
	s.OnStatus = func(s *session.Session) {
		m.history.Status(s.ID, s.Status.String(), !s.Adapter.ReportsStatus(), s.Since)
	}
}

// record adds an event about a session to the history.
func (m *Model) record(s *session.Session, e session.Event) {
	if s != nil {
		e.S = s.ID
	}
	m.history.Record(e)
}

// recordText adds an event about a text the user sent, keeping of it what
// the settings say.
func (m *Model) recordText(s *session.Session, e session.Event, text string) {
	e.N, e.X = session.Clip(text, m.cfg.Timeline.Text)
	m.record(s, e)
}

// noteKey remembers when the user last pressed a key, and from when they
// were away before it.
func (m *Model) noteKey(now time.Time) {
	if !m.lastKey.IsZero() && now.Sub(m.lastKey) >= awayAfter {
		m.awayFrom = m.lastKey
	}
	m.lastKey = now
}

// recordTurn records what the agent reported of its turn: when one starts,
// whether it is for a message cicoria sent or one the user typed; when one
// ends, the files it changed.
func (m *Model) recordTurn(s *session.Session, u agent.Update, now time.Time) {
	if u.TurnStarted {
		from := "typed"
		if at, ok := m.delivered[s.ID]; ok && now.Sub(at) < fromQueueWithin {
			from = "cicoria"
		} else {
			m.record(s, session.Event{T: now, K: session.KindPromptTyped, Src: session.FromTUI})
		}
		delete(m.delivered, s.ID)
		m.turnFrom[s.ID] = now
		m.record(s, session.Event{T: now, K: session.KindTurnStart, V: from, Src: session.FromHook})
	}
	if u.Failed {
		m.record(s, session.Event{T: now, K: session.KindTurnFailed, Src: session.FromHook})
	}
	if u.TurnEnded {
		start, ok := m.turnFrom[s.ID]
		if !ok {
			start = s.Started
		}
		var files []string
		for path, at := range s.Touched {
			if !at.Before(start) {
				files = append(files, path)
			}
		}
		slices.Sort(files)
		delete(m.turnFrom, s.ID)
		m.recordText(s, session.Event{T: now, K: session.KindTurnEnd, Src: session.FromHook, Files: files}, u.Answer)
	}
}

// recordKey records what a key the user typed into an agent did: the
// answer to a question it asked, or stopping its turn. The key itself is
// not kept.
func (m *Model) recordKey(s *session.Session, key string, now time.Time) {
	switch {
	case s.Status == agent.NeedsInput && key == "enter":
		// One question is answered once.
		if m.answered[s.ID].Equal(s.Since) {
			return
		}
		m.answered[s.ID] = s.Since
		label := ""
		if menu, ok := agent.ReadMenu(s.Term.String()); ok && menu.Selected >= 0 && menu.Selected < len(menu.Choices) {
			label = menu.Choices[menu.Selected]
		}
		m.record(s, session.Event{T: now, K: session.KindAnswer, V: label, Src: session.FromTUI})
	case s.Status == agent.Working && (key == "esc" || key == "ctrl+c"):
		m.record(s, session.Event{T: now, K: session.KindInterrupted, Src: session.FromTUI})
	}
}
