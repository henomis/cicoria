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

// fileIntent says which turn of a session last changed a file: what it was
// for, and what the agent said at its end. It is the turn under way when the
// agent last wrote the file, as this run saw it (touched), else, as after a
// restart, the last turn whose end named it. Each line ends in a newline,
// and none looks like a line of a diff.
func (m *Model) fileIntent(id, path string, touched time.Time) []string {
	var evs []session.Event
	for _, e := range m.history.Events() {
		if e.S == id {
			evs = append(evs, e)
		}
	}
	ends := func(k session.Kind) bool {
		return k == session.KindTurnEnd || k == session.KindTurnFailed || k == session.KindInterrupted
	}
	start, end := -1, -1
	if !touched.IsZero() {
		for k, e := range evs {
			if e.K == session.KindTurnStart && !e.T.After(touched) {
				start, end = k, -1
			}
			if start >= 0 && end < 0 && k > start && ends(e.K) {
				end = k
			}
		}
		// A turn that had ended when the file was written did not write it.
		if end >= 0 && evs[end].T.Before(touched) {
			start, end = -1, -1
		}
	}
	if start < 0 {
		for k, e := range evs {
			if e.K == session.KindTurnEnd && slices.Contains(e.Files, path) {
				end = k
			}
		}
		for k := end - 1; k >= 0 && end >= 0; k-- {
			if evs[k].K == session.KindTurnStart {
				start = k
				break
			}
		}
	}
	if start < 0 && end < 0 {
		return nil
	}
	// The turns before it that named the file changed it before.
	earlier, before := 0, start
	if before < 0 {
		before = end
	}
	for _, e := range evs[:before] {
		if e.K == session.KindTurnEnd && slices.Contains(e.Files, path) {
			earlier++
		}
	}

	oneLine := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	out := []string{"why   the agent changed it in a turn\n"}
	if start >= 0 {
		st := evs[start]
		why := m.turnPurpose(evs[:start], st)
		// A prompt typed in the pane is recorded just before its turn.
		for j := start - 1; j >= 0 && st.V == "typed" && st.T.Sub(evs[j].T) <= fromQueueWithin; j-- {
			if evs[j].K == session.KindPromptTyped && evs[j].X != "" {
				why = "your prompt" + quoted(evs[j])
				break
			}
		}
		out[0] = fmt.Sprintf("why   %s for %s\n", st.T.Format("15:04"), oneLine(why))
	}
	if end >= 0 && evs[end].K == session.KindTurnEnd {
		if said := oneLine(evs[end].X); said != "" {
			out = append(out, "      the agent said “"+said+"”\n")
		}
	}
	if earlier > 0 {
		out = append(out, fmt.Sprintf("      and %d earlier turn(s)\n", earlier))
	}
	return append(out, "\n")
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
// The keys that open what is under the cursor of a turn are those of the
// changes panel: d and enter its diff, v the file itself. q in what opens
// comes back here.
func (m *Model) turnKey(key string) tea.Cmd {
	tv := m.timeline.turn
	items := len(tv.files) + len(tv.commits)
	switch key {
	case "esc", "q":
		m.timeline.turn = nil
	case "j", "down":
		tv.cursor = min(tv.cursor+1, max(items-1, 0))
	case "k", "up":
		tv.cursor = max(tv.cursor-1, 0)
	case "g", "home":
		tv.cursor = 0
	case "G", "end":
		tv.cursor = max(items-1, 0)
	case "enter", "d", "v":
		s, i := m.byID(tv.id)
		switch {
		case s == nil:
			m.say(tv.name + " is closed")
			return nil
		case items == 0:
			// Nothing to open: the session itself is what there is.
			m.timeline.back = modeWorkspace
			m.closeTimeline()
			return m.switchTo(i)
		}
		// Notes written in what opens are for this session.
		cmd := m.switchTo(i)
		if tv.cursor < len(tv.files) {
			m.openTurnFile(s, tv.files[tv.cursor], key != "v")
		} else {
			m.openCommit(tv.commits[tv.cursor-len(tv.files)])
		}
		m.view.back = modeTimeline
		return cmd
	}
	return nil
}

// openTurnFile shows a file a turn changed: its diff since the session
// started, or the file itself.
func (m *Model) openTurnFile(s *session.Session, path string, asDiff bool) {
	if !asDiff {
		m.showFile(path)
		return
	}
	m.view.dir, m.view.path, m.view.diff, m.view.commit = s.Dir, path, true, nil
	m.view.output = nil
	m.view.plain, m.view.refs = nil, nil
	m.view.rendered, m.view.spans = false, nil
	m.view.pager.Title, m.view.pager.Wrap = path, m.view.wrap
	m.mode = modeView

	c := git.Change{Path: path}
	if changes, err := git.Status(s.Dir, s.Base); err == nil {
		for _, ch := range changes {
			if ch.Path == path {
				c = ch
			}
		}
	}
	diff, err := git.Diff(s.Dir, c, s.Base)
	switch {
	case err != nil:
		m.view.pager.SetContent([]string{ui.Bad.Render(err.Error())}, nil)
	case diff == "":
		m.view.pager.SetContent([]string{ui.Dim.Render("no changes since " + s.Name + " started: they were undone, or the commit it started from moved")}, nil)
	default:
		m.view.pager.SetContent(ui.DiffLines(diff))
		m.view.plain = strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
		m.view.refs = ui.DiffRefs(diff)
	}
	m.markNotes()
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
