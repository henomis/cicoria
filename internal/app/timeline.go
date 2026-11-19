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

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

const (
	// timelineNarrow is the width under which the timeline has no bars.
	timelineNarrow = 60
	// minRange is the shortest time the timeline shows.
	minRange = 30 * time.Minute
)

// timelineRanges are the times the timeline can show, back from now, past
// the time since you were away.
var timelineRanges = []time.Duration{
	30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour,
}

// sinceAway is the range of the time since you were away.
const sinceAway = -1

// timeline is what the timeline shows.
type timeline struct {
	// back is what it was opened from.
	back mode
	// rng is the time shown, one of timelineRanges or sinceAway.
	rng int
	// sel is the line under the cursor, and top the first line of the log
	// on screen.
	sel entryKey
	top int
	// turn is the turn shown over the timeline, if any.
	turn *turnView
	// frame is how long a cell of the bars lasts, kept while it fits.
	frame barFrame
	// commits are those of each session since it started, by ID, and
	// polling says they are being read.
	commits map[string][]git.Commit
	polling bool
}

// barFrame is how long a cell of the bars lasts, and what for: the width,
// the time chosen and when you were away.
type barFrame struct {
	step  time.Duration
	width int
	rng   int
	away  time.Time
}

// timelineMsg carries the commits of every session.
type timelineMsg struct{ commits map[string][]git.Commit }

// lane is what the timeline knows of one session.
type lane struct {
	id   string
	s    *session.Session
	name string
	// events are those of the session, the oldest first, and periods what
	// it did over time.
	events  []session.Event
	periods []ui.Period
}

// waiting reports whether the session of a lane waits for you now.
func (l lane) waiting() bool { return l.s != nil && l.s.Status == agent.NeedsInput }

// openTimeline shows the timeline, or goes back from it.
func (m *Model) openTimeline() tea.Cmd {
	if m.mode == modeTimeline {
		m.closeTimeline()
		return nil
	}
	return m.openTimelineOn(nil)
}

// openTimelineOn shows the timeline. The cursor is on the last thing done
// in a session, when one is given, or else on the session that has waited
// longest for you, if one waits.
func (m *Model) openTimelineOn(s *session.Session) tea.Cmd {
	m.timeline.back = modeWorkspace
	if m.mode == modeBoard || m.commandFrom == modeBoard {
		m.timeline.back = modeBoard
	}
	m.timeline.rng, m.timeline.sel, m.timeline.top, m.timeline.turn = sinceAway, entryKey{}, 0, nil
	m.timeline.frame = barFrame{}
	_, _, lines := m.timelineData(time.Now())
	for _, e := range lines {
		if s != nil && e.id == s.ID && !e.now || s == nil && e.kind == '▲' {
			m.timeline.sel = e.key()
			break
		}
	}
	m.mode = modeTimeline
	return m.pollTimeline()
}

// closeTimeline goes back to what the timeline was opened from.
func (m *Model) closeTimeline() {
	m.mode, m.timeline.turn = m.timeline.back, nil
	m.seen()
}

// timelineFrom returns when the time the timeline shows starts.
func (m *Model) timelineFrom(now time.Time) time.Time {
	if r := m.timeline.rng; r >= 0 && r < len(timelineRanges) {
		return now.Add(-timelineRanges[r])
	}
	from := m.awayFrom
	if from.IsZero() {
		from = now.Add(-time.Hour)
	}
	return minTime(from, now.Add(-minRange))
}

// pollTimeline reads, in the background, the commits of every session.
func (m *Model) pollTimeline() tea.Cmd {
	if m.timeline.polling {
		return nil
	}
	m.timeline.polling = true
	type look struct{ id, dir, base string }
	var looks []look
	for _, s := range m.sessions {
		if s.Base != "" {
			looks = append(looks, look{s.ID, s.Dir, s.Base})
		}
	}
	return func() tea.Msg {
		commits := map[string][]git.Commit{}
		for _, l := range looks {
			if git.Exists(l.dir, l.base) {
				commits[l.id], _ = git.Log(l.dir, l.base)
			}
		}
		return timelineMsg{commits}
	}
}

// laneState is how a status is drawn on a bar.
func laneState(status string) ui.State {
	switch status {
	case "working":
		return ui.StateWorking
	case "needs input":
		return ui.StateWaiting
	case "idle", "starting":
		return ui.StateIdle
	}
	return ui.StateNone
}

// sessionNames returns the name of each session the history knows, live or
// closed.
func (m *Model) sessionNames(events []session.Event) map[string]string {
	names := map[string]string{}
	for _, e := range events {
		switch e.K {
		case session.KindSessionOpen, session.KindRename, session.KindSessionClose:
			names[e.S] = e.V
		}
	}
	for _, s := range m.sessions {
		names[s.ID] = s.Name
	}
	return names
}

// timelineData returns how the bars are drawn, the lanes of the sessions
// with something to show in that time, and the lines under the bars: the
// sessions that need you or work now, then what you did, the newest first.
func (m *Model) timelineData(now time.Time) (ui.Timeline, []lane, []entry) {
	from := m.timelineFrom(now)
	events := m.history.Events()
	names := m.sessionNames(events)
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return "a closed session"
	}
	byID := map[string][]session.Event{}
	var runEnds []time.Time
	for k, e := range events {
		if e.S == "" {
			switch {
			case e.K == session.KindRunEnd:
				runEnds = append(runEnds, e.T)
			case e.K == session.KindRunStart && k > 0 && events[k-1].K != session.KindRunEnd:
				// The run before ended without saying so, as when the
				// terminal was closed: after the last thing it recorded.
				runEnds = append(runEnds, events[k-1].T)
			}
			continue
		}
		byID[e.S] = append(byID[e.S], e)
	}
	ids := []string{}
	for _, s := range m.sessions {
		ids = append(ids, s.ID)
	}
	for id, evs := range byID {
		if !slices.Contains(ids, id) && !evs[len(evs)-1].T.Before(from) {
			ids = append(ids, id)
		}
	}

	width := 0
	for _, id := range ids {
		width = max(width, ansi.StringWidth(name(id)))
	}
	t := ui.Timeline{From: from, Now: now, Width: m.width, LabelWidth: min(width, 16) + 5}
	t.From, t.Step = m.layOut(from, now, t.Cells())

	var lanes []lane
	var log []entry
	for _, id := range ids {
		s, _ := m.byID(id)
		l := lane{id: id, s: s, name: name(id), events: byID[id]}
		l.periods = periods(l.events, runEnds)
		said := 0
		for k, e := range l.events {
			if e.T.Before(from) {
				continue
			}
			if line, ok := l.say(k, name, now); ok {
				log = append(log, line)
				said++
			}
		}
		for _, c := range m.timeline.commits[id] {
			if !c.When.Before(from) {
				log = append(log, entry{at: c.When, id: id, name: l.name, kind: '◆', text: "commit " + c.Short + " " + c.Subject, span: ui.Period{From: c.When, To: c.When}})
				said++
			}
		}
		// A session that did nothing in that time is left out of the bars.
		busy := s != nil && (s.Status == agent.Working || s.Status == agent.NeedsInput)
		if said > 0 || busy || busyIn(t, l) {
			lanes = append(lanes, l)
		}
	}
	slices.SortStableFunc(log, func(a, b entry) int { return b.at.Compare(a.at) })
	return t, lanes, append(nowLines(lanes, now), log...)
}

// layOut returns where the bars start and how long a cell lasts, for the
// time from from to now in cells. Now is in the last cell, and the cells
// start at whole multiples of their length: as time passes the bars move
// left a whole cell at a time, and are never cut at other times. The
// length is kept while the time shown and the width stay.
func (m *Model) layOut(from, now time.Time, cells int) (time.Time, time.Duration) {
	cells = max(cells, 1)
	f := m.timeline.frame
	if f.step <= 0 || f.width != m.width || f.rng != m.timeline.rng || !f.away.Equal(m.awayFrom) {
		step := max((now.Sub(from)+time.Duration(cells)-1)/time.Duration(cells), time.Second)
		f = barFrame{step: step.Truncate(time.Second) + time.Second, width: m.width, rng: m.timeline.rng, away: m.awayFrom}
		m.timeline.frame = f
	}
	return now.Truncate(f.step).Add(-time.Duration(cells-1) * f.step), f.step
}

// periods returns what a session did over time, from its events: the
// statuses it was in, the turns that ended or failed, and nothing while
// cicoria was not running or once it was closed.
func periods(events []session.Event, runEnds []time.Time) []ui.Period {
	var out []ui.Period
	open := -1
	stop := func(at time.Time) {
		if open >= 0 {
			out[open].To = at
			open = -1
		}
	}
	ends := slices.Clone(runEnds)
	for _, e := range events {
		// cicoria was closed in between.
		for len(ends) > 0 && !ends[0].After(e.T) {
			stop(ends[0])
			ends = ends[1:]
		}
		switch e.K {
		case session.KindStatus:
			stop(e.T)
			if st := laneState(e.V); st != ui.StateNone {
				out = append(out, ui.Period{From: e.T, State: st})
				open = len(out) - 1
			}
		case session.KindSessionClose:
			stop(e.T)
		case session.KindTurnEnd:
			out = append(out, ui.Period{From: e.T, To: e.T, State: ui.StateDone})
		case session.KindTurnFailed:
			out = append(out, ui.Period{From: e.T, To: e.T, State: ui.StateFailed})
		}
	}
	// An open period of a session that was closed with cicoria ends then.
	if len(ends) > 0 {
		stop(ends[0])
	}
	return out
}

// cursor returns where the line under the cursor is. A line that is gone,
// as out of the time shown, gives way to the one nearest to it in time; a
// session that no longer waits or works, to the newest thing you did.
func (m *Model) cursor(lines []entry) int {
	if len(lines) == 0 {
		return -1
	}
	sel := m.timeline.sel
	i := slices.IndexFunc(lines, func(e entry) bool { return e.key() == sel })
	if i < 0 {
		i = slices.IndexFunc(lines, func(e entry) bool { return !e.now })
		if i < 0 {
			i = 0
		}
		if !sel.at.IsZero() {
			for k, e := range lines {
				if !e.now && e.at.Sub(sel.at).Abs() < lines[i].at.Sub(sel.at).Abs() {
					i = k
				}
			}
		}
		m.timeline.sel = lines[i].key()
	}
	return i
}

// timelineKey handles the keys of the timeline.
func (m *Model) timelineKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if m.prefix {
		m.prefix = false
		// The key that opened the timeline closes it.
		if key == "e" {
			m.closeTimeline()
			return nil
		}
		return m.runPrefix(k)
	}
	if key == m.cfg.Prefix {
		m.prefix = true
		return nil
	}
	if m.timeline.turn != nil {
		return m.turnKey(key)
	}

	now := time.Now()
	_, lanes, lines := m.timelineData(now)
	i := m.cursor(lines)
	move := func(to int) {
		if len(lines) > 0 {
			m.timeline.sel = lines[max(min(to, len(lines)-1), 0)].key()
		}
	}

	switch key {
	case "esc", "q":
		m.closeTimeline()
	case "j", "down":
		move(i + 1)
	case "k", "up":
		move(i - 1)
	case "g", "home":
		move(0)
	case "G", "end":
		move(len(lines) - 1)
	case "+", "=":
		m.zoomTimeline(-1, now)
	case "-":
		m.zoomTimeline(1, now)
	case "s":
		m.timeline.rng = sinceAway
	case "a":
		// The next session that waits for you, round the list.
		var waits []int
		for k, e := range lines {
			if e.kind == '▲' {
				waits = append(waits, k)
			}
		}
		if len(waits) == 0 {
			m.say("no session needs you")
			return nil
		}
		next := waits[0]
		for _, k := range waits {
			if k > i {
				next = k
				break
			}
		}
		move(next)
	case "enter":
		if i < 0 {
			return nil
		}
		s, j := m.byID(lines[i].id)
		if s == nil {
			m.say(lines[i].name + " is closed")
			return nil
		}
		m.timeline.back = modeWorkspace
		m.closeTimeline()
		return m.switchTo(j)
	case "d", "space", " ":
		if i < 0 {
			return nil
		}
		k := slices.IndexFunc(lanes, func(l lane) bool { return l.id == lines[i].id })
		if k < 0 {
			return nil
		}
		m.timeline.turn = m.turnAt(lanes[k], lines[i].at, now)
		if m.timeline.turn == nil {
			m.say("no turn of " + lanes[k].name + " to show there")
		}
	case "m":
		if i >= 0 {
			if s, _ := m.byID(lines[i].id); s != nil {
				return m.askQueueFor(s)
			}
		}
	}
	return nil
}

// zoomTimeline shows a shorter or a longer time, back from now.
func (m *Model) zoomTimeline(delta int, now time.Time) {
	r := m.timeline.rng
	if r == sinceAway {
		// From the time since you were away, to the nearest range on that
		// side of it.
		shown := now.Sub(m.timelineFrom(now))
		r = len(timelineRanges)
		for k, d := range timelineRanges {
			if d >= shown {
				r = k
				break
			}
		}
		if delta > 0 && r < len(timelineRanges) && timelineRanges[r] > shown {
			delta = 0
		}
	}
	m.timeline.rng = max(min(r+delta, len(timelineRanges)-1), 0)
}

// timelineTitle says what time the timeline shows.
func (m *Model) timelineTitle(now time.Time) string {
	if r := m.timeline.rng; r >= 0 && r < len(timelineRanges) {
		return "the last " + span(timelineRanges[r])
	}
	if m.awayFrom.IsZero() || now.Sub(m.awayFrom) < minRange {
		return "the last " + span(now.Sub(m.timelineFrom(now)))
	}
	return "since you left at " + m.awayFrom.Format("15:04")
}

// renderTimeline draws the timeline over the whole screen but the status
// bar: what time it shows and what it comes to; the bars of the sessions;
// what needs you or works now; and the log of what you did, the newest
// first. A rule with the legend closes it.
func (m *Model) renderTimeline() []string {
	height := max(m.height-1, 2)
	now := time.Now()
	t, lanes, lines := m.timelineData(now)
	i := m.cursor(lines)
	var sel *entry
	if i >= 0 {
		sel = &lines[i]
	}

	rows := []string{ui.Reverse.Render(ui.Fit(m.timelineHeader(t, lanes, now), m.width))}

	if m.width >= timelineNarrow && len(lanes) > 0 {
		// The bars take at most half of the screen; those that wait for
		// you and the one of the line under the cursor stay.
		fits := max((height-6)/2, 1)
		var shown []lane
		for _, l := range lanes {
			if len(shown) < fits-1 || l.waiting() || sel != nil && l.id == sel.id {
				shown = append(shown, l)
			}
		}
		if len(shown) == len(lanes)-1 && len(shown) < fits {
			shown = lanes
		}
		var at time.Time
		if sel != nil {
			at = sel.span.From
			if at.IsZero() {
				at = sel.at
			}
		}
		rows = append(rows, t.Axis(at))
		bars := make([]ui.Bar, len(shown))
		for k, l := range shown {
			label := l.name
			if l.s != nil {
				mark := " "
				if !l.s.Adapter.ReportsStatus() {
					mark = "~"
				}
				label = ui.Fit(l.name, t.LabelWidth-5) + " " + statusGlyph(l.s) + mark
			}
			bars[k] = ui.Bar{Label: label, Periods: l.periods, Dim: l.s == nil}
			if sel != nil && l.id == sel.id {
				bars[k].Selected, bars[k].Span = true, sel.span
			}
		}
		rows = append(rows, t.Render(bars)...)
		if more := len(lanes) - len(shown); more > 0 {
			rows = append(rows, ui.Dim.Render(ui.Fit(fmt.Sprintf(" +%d more, quiet or not in sight", more), m.width)))
		}
	}
	rule := ui.Dim.Render(strings.Repeat("─", max(m.width, 0)))
	rows = append(rows, rule)

	nameW := 0
	for _, e := range lines {
		nameW = max(nameW, min(ansi.StringWidth(e.name), 16))
	}
	draw := func(k int) string {
		e := lines[k]
		when := e.at.Format("15:04")
		switch {
		case e.now:
			when = "now  "
		case now.Sub(e.at) > 20*time.Hour:
			when = e.at.Format("Mon 15:04")
		}
		kind, text, outcome := string(e.kind), e.text, e.outcome
		if k != i {
			switch {
			case e.kind == '▲':
				kind, text = ui.Warn.Render(kind), ui.Warn.Render(text)
			case e.kind == '✕':
				kind, text = ui.Bad.Render(kind), ui.Bad.Render(text)
			case e.bad:
				outcome = ui.Bad.Render(outcome)
			case e.kind == '◆' || e.kind == '●':
				kind, text = ui.Dim.Render(kind), ui.Dim.Render(text)
			default:
				kind = ui.Accent.Render(kind)
			}
		}
		lead := " "
		if k == i {
			lead = "▸"
		}
		return ui.Selected(lead+when+" "+ui.Fit(e.name, nameW)+" "+kind+" "+text+outcome, m.width, k == i, true)
	}

	// What needs you or works now stays in sight.
	first := slices.IndexFunc(lines, func(e entry) bool { return !e.now })
	if first < 0 {
		first = len(lines)
	}
	for k := range first {
		rows = append(rows, draw(k))
	}
	if first > 0 {
		rows = append(rows, rule)
	}

	// What the agent said at the end of the turn of the line under the
	// cursor is under it.
	said := ""
	if i >= first && sel != nil && sel.said != "" {
		said = ui.Dim.Render(ui.Fit(strings.Repeat(" ", 10+nameW)+"it said: "+sel.said, m.width))
	}
	room := max(height-len(rows)-1, 1)
	if said != "" {
		room = max(room-1, 1)
	}
	log := lines[first:]
	var out []string
	if len(log) == 0 {
		out = append(out, ui.Dim.Render(ui.Fit("  Nothing from you in "+m.timelineTitle(now)+". - shows a longer time.", m.width)))
	}
	cur := max(i-first, 0)
	m.timeline.top = ui.Window(cur, m.timeline.top, room, len(log))
	for k := m.timeline.top; k < min(len(log), m.timeline.top+room); k++ {
		out = append(out, draw(first+k))
		if first+k == i && said != "" {
			out = append(out, said)
		}
	}
	rows = append(rows, out...)
	rows = append(rows, m.timelineLegend())
	// The rest of the screen is empty, under the legend.
	for len(rows) < height {
		rows = append(rows, "")
	}
	rows = rows[:min(len(rows), height)]

	// A screen too short for the turn shows it once it grows.
	if m.timeline.turn != nil {
		if box := m.renderTurn(min(m.width-4, 76), height-2); len(box) > 0 {
			rows = ui.Overlay(rows, box, (m.width-ansi.StringWidth(box[0]))/2, max((height-len(box))/2, 0))
		}
	}
	return rows
}

// timelineHeader says what time the timeline shows, how long agents waited
// for you in it, and what else is left for you: notes not sent, messages
// queued. Who waits now is said under the bars.
func (m *Model) timelineHeader(t ui.Timeline, lanes []lane, now time.Time) string {
	from := m.timelineFrom(now)
	parts := []string{" timeline  " + from.Format("15:04") + " → now, " + span(now.Sub(from))}
	if m.timeline.rng == sinceAway && !m.awayFrom.IsZero() && now.Sub(m.awayFrom) >= minRange {
		parts[0] += ", since you were away"
	}
	var waited time.Duration
	for _, l := range lanes {
		for _, p := range l.periods {
			if p.State != ui.StateWaiting {
				continue
			}
			to := p.To
			if to.IsZero() {
				to = now
			}
			if from := maxTime(p.From, t.From); to.After(from) {
				waited += to.Sub(from)
			}
		}
	}
	if waited >= time.Minute {
		parts = append(parts, "agents waited for you "+span(waited))
	}
	var notes, queued int
	for _, s := range m.sessions {
		notes += len(s.Notes)
		queued += len(s.Queue)
	}
	if notes > 0 {
		parts = append(parts, fmt.Sprintf("%d note(s) not sent", notes))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d message(s) queued", queued))
	}
	return strings.Join(parts, " · ")
}

// timelineLegend is the rule under the timeline, with what the bars and
// the marks of the lines mean, as much of it as fits.
func (m *Model) timelineLegend() string {
	legend := " ▄ working █ waits for you ─ idle ✓ done ✕ failed ~ guessed · › message ↵ answer ? ask □ card ◆ commit "
	w := ansi.StringWidth(legend)
	if w+4 > m.width {
		legend = " ▄ working █ waits ─ idle ✓ done ✕ failed "
		w = ansi.StringWidth(legend)
	}
	if w+4 > m.width {
		return ui.Dim.Render(strings.Repeat("─", max(m.width, 0)))
	}
	return ui.Dim.Render("──" + legend + strings.Repeat("─", m.width-w-2))
}

// statusGlyph is the status mark of a session without its colour.
func statusGlyph(s *session.Session) string { return ansi.Strip(badge(s)) }

// timelineHint is what the status bar says the keys of the timeline do.
func (m *Model) timelineHint() string {
	if m.timeline.turn != nil {
		return "j/k move · d/enter diff · v file · q back"
	}
	return "j/k move · a next waiting · d turn · enter go · m queue · +/- time · q back"
}

// span says how long a time is, to the minute, as "1h20m".
func span(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	d = d.Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
