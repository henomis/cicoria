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
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// turnView is one turn of a session, shown over the timeline: what it was
// for, what changed in it and what the agent said at its end.
type turnView struct {
	id, name   string
	start, end time.Time
	// open says the turn is not over, and guessed that its bounds are
	// guessed from the agent's output, which does not say when it works.
	open, guessed bool
	// from is what the turn was for, and said what the agent said last.
	from, said string
	files      []string
	commits    []git.Commit
	// cursor is on one of the files, then the commits.
	cursor int
}

// turnAt returns the turn of a lane at a moment: the one under way then,
// else the one that starts soon after it, as what the user did led to,
// else the last one before it.
func (m *Model) turnAt(l lane, at, now time.Time) *turnView {
	var turns []*turnView
	for k, e := range l.events {
		if e.K != session.KindTurnStart {
			continue
		}
		tv := &turnView{id: l.id, name: l.name, start: e.T, end: now, open: true}
		tv.from = m.turnPurpose(l.events[:k], e)
	scan:
		for _, f := range l.events[k+1:] {
			switch f.K {
			case session.KindTurnEnd:
				tv.files, tv.said = f.Files, f.X
			case session.KindTurnFailed:
				tv.said = "the turn ended on an error"
			case session.KindInterrupted:
				tv.said = "you stopped the turn"
			case session.KindTurnStart:
			default:
				continue
			}
			tv.end, tv.open = f.T, false
			break scan
		}
		turns = append(turns, tv)
	}
	// An agent that does not say when it works has turns of its output.
	if len(turns) == 0 {
		for _, p := range l.periods {
			if p.State == ui.StateWorking {
				tv := &turnView{id: l.id, name: l.name, start: p.From, end: p.To, open: p.To.IsZero(), guessed: true}
				if tv.open {
					tv.end = now
				}
				turns = append(turns, tv)
			}
		}
	}

	var pick *turnView
	for _, tv := range turns {
		switch {
		case !tv.start.After(at) && !tv.end.Before(at):
			pick = tv
		case pick == nil && tv.start.After(at) && tv.start.Sub(at) <= turnSoon:
			pick = tv
		}
	}
	if pick == nil {
		for _, tv := range turns {
			if tv.end.Before(at) {
				pick = tv
			}
		}
	}
	if pick == nil {
		return nil
	}
	for _, c := range m.timeline.commits[l.id] {
		// Commits are told to the second.
		if !c.When.Before(pick.start.Truncate(time.Second)) && !c.When.After(pick.end) {
			pick.commits = append(pick.commits, c)
		}
	}
	return pick
}

// turnSoon is how soon after what the user did a turn that starts is taken
// to be the one it led to.
const turnSoon = 2 * time.Minute

// turnPurpose says what a turn that started was for, from the events before
// it.
func (m *Model) turnPurpose(before []session.Event, start session.Event) string {
	if start.V == "typed" {
		return "a prompt you typed in the agent's pane"
	}
	for k := len(before) - 1; k >= 0; k-- {
		e := before[k]
		if start.T.Sub(e.T) > fromQueueWithin {
			break
		}
		if e.K == session.KindSend {
			return sentFrom(e.V) + quoted(e)
		}
	}
	return "a message from cicoria"
}

// turnKey handles the keys of the turn shown over the timeline.
func (m *Model) turnKey(key string) tea.Cmd {
	tv := m.timeline.turn
	items := len(tv.files) + len(tv.commits)
	switch key {
	case "j", "down":
		tv.cursor = min(tv.cursor+1, max(items-1, 0))
	case "k", "up":
		tv.cursor = max(tv.cursor-1, 0)
	case "enter":
		s, i := m.byID(tv.id)
		if s == nil || items == 0 {
			return nil
		}
		m.timeline.back = modeWorkspace
		m.closeTimeline()
		cmd := m.switchTo(i)
		if tv.cursor < len(tv.files) {
			m.tree().Reveal(tv.files[tv.cursor])
			m.showFile(tv.files[tv.cursor])
		} else {
			m.openCommit(tv.commits[tv.cursor-len(tv.files)])
		}
		return cmd
	default:
		m.timeline.turn = nil
	}
	return nil
}

// renderTurn draws the turn shown over the timeline in a box of width,
// at most height rows.
func (m *Model) renderTurn(width, height int) []string {
	tv := m.timeline.turn
	inner := width - 2
	when := tv.start.Format("15:04") + " → " + tv.end.Format("15:04")
	if tv.open {
		when = tv.start.Format("15:04") + " → now"
	}
	lines := []string{" " + when + " · " + span(tv.end.Sub(tv.start))}
	if tv.guessed {
		lines = append(lines, ui.Dim.Render(" this agent does not say when it works: the turn is guessed from its screen"))
	} else {
		lines = append(lines, " for "+tv.from)
	}
	if tv.said != "" {
		lines = append(lines, "", ui.Dim.Render(" it said: ")+tv.said)
	}
	k := 0
	item := func(text string) string {
		selected := k == tv.cursor
		k++
		lead := "   "
		if selected {
			lead = " ▸ "
		}
		return ui.Selected(lead+text, inner, selected, true)
	}
	if len(tv.files) > 0 {
		lines = append(lines, "", fmt.Sprintf(" changed %d file(s)", len(tv.files)))
		for _, f := range tv.files {
			lines = append(lines, item(f))
		}
	} else if !tv.guessed && !tv.open {
		lines = append(lines, "", ui.Dim.Render(" no file was changed through the agent's tools"))
	}
	if len(tv.commits) > 0 {
		lines = append(lines, "", fmt.Sprintf(" %d commit(s)", len(tv.commits)))
		for _, c := range tv.commits {
			lines = append(lines, item(c.Short+" "+c.Subject))
		}
	}
	h := min(len(lines)+2, height)
	// The cursor stays in sight.
	if over := len(lines) - (h - 2); over > 0 {
		lines = lines[min(over, max(tv.cursor, 0)):]
	}
	return ui.Box(tv.name+" · turn", lines, width, h, true)
}
