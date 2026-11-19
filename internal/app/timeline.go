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
	// timelineNarrow is the width under which the timeline is a list of
	// what happened, and timelineWide the one from which its labels are
	// wider.
	timelineNarrow = 60
	timelineWide   = 100
	// timelineFooter is how many rows are under the lanes.
	timelineFooter = 3
)

// timelineZooms are how long a cell of the timeline can last.
var timelineZooms = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// foldedLane is the lane cursor on the line of the quiet sessions.
const foldedLane = "\x00folded"

// timeline is what the timeline shows.
type timeline struct {
	// back is what it was opened from.
	back mode
	zoom int
	// end is the time at its right edge, or zero while it follows now.
	end time.Time
	// on is the session whose lane has the cursor, and at the moment
	// whose marks are picked, or zero.
	on string
	at time.Time
	// unfold shows the quiet sessions too.
	unfold bool
	// turn is the turn shown over the timeline, if any.
	turn *turnView
	// commits are those of each session since it started, by ID, and
	// polling says they are being read.
	commits map[string][]git.Commit
	polling bool
}

// timelineMsg carries the commits of every session.
type timelineMsg struct{ commits map[string][]git.Commit }

// lane is one session on the timeline.
type lane struct {
	id   string
	s    *session.Session
	name string
	ui   ui.Lane
	// events are those of the session, the oldest first, and marks those
	// shown in the timeline's time, each with what it says.
	events []session.Event
	marks  []laneMark
	last   time.Time
	quiet  bool
}

// laneMark is a mark on a lane and what it stands for.
type laneMark struct {
	at    time.Time
	glyph rune
	text  string
}

// openTimeline shows the timeline, or goes back from it.
func (m *Model) openTimeline() tea.Cmd {
	if m.mode == modeTimeline {
		m.closeTimeline()
		return nil
	}
	return m.openTimelineOn(m.current())
}

// openTimelineOn shows the timeline with the cursor on a session's lane.
func (m *Model) openTimelineOn(s *session.Session) tea.Cmd {
	m.timeline.back = modeWorkspace
	if m.mode == modeBoard {
		m.timeline.back = modeBoard
	}
	m.timeline.end, m.timeline.at, m.timeline.turn, m.timeline.unfold = time.Time{}, time.Time{}, nil, false
	m.timeline.on = ""
	if s != nil {
		m.timeline.on = s.ID
	}
	m.timeline.zoom = m.fitZoom(time.Now())
	m.mode = modeTimeline
	return m.pollTimeline()
}

// closeTimeline goes back to what the timeline was opened from.
func (m *Model) closeTimeline() {
	m.mode, m.timeline.turn = m.timeline.back, nil
	m.seen()
}

// fitZoom returns the smallest zoom that shows the time since the user was
// last away, and at least an hour.
func (m *Model) fitZoom(now time.Time) int {
	span := time.Hour
	if !m.awayFrom.IsZero() {
		span = max(span, now.Sub(m.awayFrom))
	}
	cells := m.timelineFrame(0, now).Cells()
	for i, step := range timelineZooms {
		if time.Duration(cells)*step >= span {
			return i
		}
	}
	return len(timelineZooms) - 1
}

// timelineFrame returns how the timeline is drawn at a zoom.
func (m *Model) timelineFrame(zoom int, now time.Time) ui.Timeline {
	label := 10
	if m.width >= timelineWide {
		label = 16
	}
	t := ui.Timeline{Step: timelineZooms[zoom], Now: now, Width: m.width, LabelWidth: label, Cell: -1}
	end := m.timeline.end
	if end.IsZero() || end.After(now) {
		end = now
	}
	// Now is in the last cell, and cells start on round times.
	last := end.Truncate(t.Step)
	t.From = last.Add(-time.Duration(t.Cells()-1) * t.Step)
	return t
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

// laneState is how a status is drawn on a lane.
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

// sentFrom names where a message came from, as the timeline says it.
func sentFrom(from string) string {
	switch from {
	case fromNotes:
		return "review notes"
	case fromCard:
		return "the card's prompt"
	case fromAsk:
		return "a message for another session"
	case fromAnswer:
		return "the answer of another session"
	}
	return "a message"
}

// quoted returns the kept text of an event between quotes, if any.
func quoted(e session.Event) string {
	if e.X == "" {
		return ""
	}
	return " “" + e.X + "”"
}

// markOf returns the mark an event puts on its lane, and what it says, if
// it is something the user did.
func (m *Model) markOf(e session.Event, name func(id string) string) (rune, string, bool) {
	web := ""
	if e.Src == session.FromWeb {
		web = " from the web"
	}
	switch e.K {
	case session.KindAnswer:
		if e.V == "" {
			return '↵', "answered" + web, true
		}
		return '↵', "answered “" + e.V + "”" + web, true
	case session.KindPromptTyped:
		return 'm', "typed a prompt", true
	case session.KindQueue, session.KindSend:
		// What the queue sends by itself was marked when it was queued.
		if e.K == session.KindSend && e.Src == "" {
			return 0, "", false
		}
		verb := "queued "
		if e.K == session.KindSend {
			verb = "sent "
		}
		switch e.V {
		case fromNotes:
			return 't', verb + "review notes" + web, true
		case fromAsk:
			return 'k', verb + "a message from another session" + quoted(e), true
		case fromAnswer:
			return 'k', "got the answer of another session", true
		case fromCard:
			return 0, "", false
		}
		to := ""
		if e.To != "" {
			to = " asking " + name(e.To)
		}
		return 'm', verb + "a message" + to + web + quoted(e), true
	case session.KindInterrupted:
		return 'x', "stopped the turn" + web, true
	case session.KindUnqueue:
		return 'm', "took back a queued message" + web + quoted(e), true
	case session.KindWorktreeAdd:
		return 'o', "made the worktree " + tilde(e.V), true
	case session.KindCardStart:
		return '◆', "started the card “" + e.V + "”", true
	}
	return 0, "", false
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

// timelineLanes returns the lanes of the sessions in the time t shows:
// those that wait for the user first, then by what happened last. Quiet
// ones are folded, unless they are unfolded, and their number returned.
func (m *Model) timelineLanes(t ui.Timeline) (lanes []lane, folded int) {
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
	for _, e := range events {
		if e.S == "" {
			if e.K == session.KindRunEnd {
				runEnds = append(runEnds, e.T)
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
		if !slices.Contains(ids, id) && !evs[len(evs)-1].T.Before(t.From) {
			ids = append(ids, id)
		}
	}

	end := t.CellTime(t.Cells())
	for _, id := range ids {
		s, _ := m.byID(id)
		l := lane{id: id, s: s, name: name(id), events: byID[id]}
		l.ui = ui.Lane{Label: l.name, Dim: s == nil}
		l.ui.Periods = periods(l.events, runEnds)
		for _, e := range l.events {
			l.last = e.T
			if e.T.Before(t.From) || !e.T.Before(end) {
				continue
			}
			if g, text, ok := m.markOf(e, name); ok {
				l.marks = append(l.marks, laneMark{e.T, g, text})
			}
		}
		for _, c := range m.timeline.commits[id] {
			if !c.When.Before(t.From) && c.When.Before(end) {
				l.marks = append(l.marks, laneMark{c.When, '●', "commit " + c.Short + " " + c.Subject})
			}
		}
		slices.SortStableFunc(l.marks, func(a, b laneMark) int { return a.at.Compare(b.at) })
		for _, mk := range l.marks {
			l.ui.Marks = append(l.ui.Marks, ui.Mark{At: mk.at, Glyph: mk.glyph})
		}
		l.ui.Note = laneNote(l)
		busy := s != nil && (s.Status == agent.Working || s.Status == agent.NeedsInput)
		l.quiet = len(l.marks) == 0 && !busy && !slices.ContainsFunc(t.States(l.ui.Periods), func(st ui.State) bool { return st > ui.StateIdle })
		lanes = append(lanes, l)
	}

	slices.SortStableFunc(lanes, func(a, b lane) int {
		if wa, wb := a.waiting(), b.waiting(); wa != wb {
			if wa {
				return -1
			}
			return 1
		}
		if (a.s == nil) != (b.s == nil) {
			if a.s == nil {
				return 1
			}
			return -1
		}
		return b.last.Compare(a.last)
	})
	if !m.timeline.unfold {
		shown := lanes[:0]
		for _, l := range lanes {
			// The lane with the cursor stays.
			if l.quiet && l.id != m.timeline.on {
				folded++
				continue
			}
			shown = append(shown, l)
		}
		lanes = shown
	}
	return lanes, folded
}

// waiting reports whether the session of a lane waits for the user now.
func (l lane) waiting() bool { return l.s != nil && l.s.Status == agent.NeedsInput }

// laneNote is what a lane says under its name: what its session does now.
func laneNote(l lane) string {
	if l.s == nil {
		return "closed"
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s", statusGlyph(l.s), lasting(l.s)))
}

// statusGlyph is the status mark of a session without its colour, which
// the lane's note does not need.
func statusGlyph(s *session.Session) string { return ansi.Strip(badge(s)) }

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

// timelineKey handles the keys of the timeline.
func (m *Model) timelineKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if m.timeline.turn != nil {
		return m.turnKey(key)
	}
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

	now := time.Now()
	t := m.timelineFrame(m.timeline.zoom, now)
	lanes, folded := m.timelineLanes(t)
	i := slices.IndexFunc(lanes, func(l lane) bool { return l.id == m.timeline.on })
	ids := make([]string, 0, len(lanes)+1)
	for _, l := range lanes {
		ids = append(ids, l.id)
	}
	if folded > 0 || m.timeline.unfold {
		ids = append(ids, foldedLane)
	}
	if m.timeline.on == foldedLane {
		i = len(ids) - 1
	}

	switch key {
	case "esc", "q":
		m.closeTimeline()
	case "j", "down", "k", "up":
		if len(ids) == 0 {
			return nil
		}
		step := 1
		if key == "k" || key == "up" {
			step = -1
		}
		m.timeline.on = ids[max(min(i+step, len(ids)-1), 0)]
		m.timeline.at = time.Time{}
	case "h", "left", "l", "right":
		if i < 0 || i >= len(lanes) {
			return nil
		}
		m.stepMark(t, lanes[i], key == "l" || key == "right")
	case "H", "L":
		half := time.Duration(t.Cells()/2) * t.Step
		end := m.timeline.end
		if end.IsZero() {
			end = now
		}
		if key == "H" {
			end = end.Add(-half)
		} else {
			end = end.Add(half)
		}
		m.timeline.end = end
		if !end.Before(now) {
			m.timeline.end = time.Time{}
		}
	case "+", "=":
		m.zoomTimeline(-1, now)
	case "-":
		m.zoomTimeline(1, now)
	case "s":
		m.timeline.end, m.timeline.at = time.Time{}, time.Time{}
		m.timeline.zoom = m.fitZoom(now)
	case "G", "end":
		m.timeline.end = time.Time{}
	case "a":
		for _, l := range lanes {
			if l.waiting() {
				m.timeline.on, m.timeline.at = l.id, time.Time{}
				return nil
			}
		}
		m.say("no session needs you")
	case "enter":
		if m.timeline.on == foldedLane {
			m.timeline.unfold = !m.timeline.unfold
			return nil
		}
		if i < 0 || i >= len(lanes) {
			return nil
		}
		if lanes[i].s == nil {
			m.say(lanes[i].name + " is closed")
			return nil
		}
		_, j := m.byID(lanes[i].id)
		m.timeline.back = modeWorkspace
		m.closeTimeline()
		return m.switchTo(j)
	case "d", "space", " ":
		if i < 0 || i >= len(lanes) {
			return nil
		}
		at := m.timeline.at
		if at.IsZero() {
			at = now
		}
		m.timeline.turn = m.turnAt(lanes[i], at, now)
		if m.timeline.turn == nil {
			m.say("no turn of " + lanes[i].name + " to show here")
		}
	case "m":
		if i >= 0 && i < len(lanes) && lanes[i].s != nil {
			return m.askQueueFor(lanes[i].s)
		}
	}
	return nil
}

// zoomTimeline shows cells of the next zoom, keeping the picked moment in
// sight.
func (m *Model) zoomTimeline(delta int, now time.Time) {
	z := max(min(m.timeline.zoom+delta, len(timelineZooms)-1), 0)
	if z == m.timeline.zoom {
		return
	}
	m.timeline.zoom = z
	if at := m.timeline.at; !at.IsZero() {
		t := m.timelineFrame(z, now)
		if c := t.CellAt(at); c < 0 || c >= t.Cells() {
			m.timeline.end = at.Add(time.Duration(t.Cells()/2) * t.Step)
			if !m.timeline.end.Before(now) {
				m.timeline.end = time.Time{}
			}
		}
	}
}

// stepMark picks the cell with marks before or after the one picked on a
// lane, scrolling the timeline to it.
func (m *Model) stepMark(t ui.Timeline, l lane, next bool) {
	if len(l.marks) == 0 {
		return
	}
	cell := t.Cells()
	if !m.timeline.at.IsZero() {
		cell = t.CellAt(m.timeline.at)
	} else if next {
		cell = -1
	}
	var pick *laneMark
	for k := range l.marks {
		c := t.CellAt(l.marks[k].at)
		if next && c > cell {
			pick = &l.marks[k]
			break
		}
		if !next && c < cell {
			pick = &l.marks[k]
		}
	}
	if pick != nil {
		m.timeline.at = pick.at
	}
}

// renderTimeline draws the timeline over the whole screen but the status
// bar.
func (m *Model) renderTimeline() []string {
	height := max(m.height-1, 2)
	now := time.Now()
	t := m.timelineFrame(m.timeline.zoom, now)
	lanes, folded := m.timelineLanes(t)

	header := " timeline  "
	if !m.awayFrom.IsZero() {
		header += "you left at " + m.awayFrom.Format("15:04") + " · "
	}
	header += cellName(timelineZooms[m.timeline.zoom]) + " a cell · "
	if m.timeline.end.IsZero() {
		header += "following now"
	} else {
		header += t.From.Format("Jan 2 15:04") + " – " + t.CellTime(t.Cells()).Format("15:04")
	}
	rows := []string{ui.Reverse.Render(ui.Fit(header, m.width))}

	if m.width < timelineNarrow {
		return append(rows, m.renderTimelineList(lanes, height-1)...)
	}

	i := slices.IndexFunc(lanes, func(l lane) bool { return l.id == m.timeline.on })
	if i < 0 && m.timeline.on != foldedLane && len(lanes) > 0 {
		i, m.timeline.on = 0, lanes[0].id
	}
	t.Lane = i
	if i >= 0 && !m.timeline.at.IsZero() {
		t.Cell = t.CellAt(m.timeline.at)
	}

	rows = append(rows, t.Axis())
	room := height - len(rows) - timelineFooter - 1
	fits := max(room/2, 1)
	uiLanes := make([]ui.Lane, len(lanes))
	for k, l := range lanes {
		uiLanes[k] = l.ui
	}
	top := ui.Window(max(i, 0), 0, fits, len(lanes))
	if len(lanes) == 0 {
		rows = append(rows, ui.Dim.Render(ui.Fit("  nothing happened in this time: - zooms out, H goes back", m.width)))
	}
	drawn := t
	drawn.Lane = i - top
	rows = append(rows, drawn.Render(uiLanes[top:min(top+fits, len(uiLanes))])...)

	switch {
	case folded > 0:
		line := fmt.Sprintf("  ··· %d quiet session(s): enter shows them", folded)
		rows = append(rows, ui.Selected(line, m.width, m.timeline.on == foldedLane, true))
	case m.timeline.unfold:
		rows = append(rows, ui.Selected("  ··· enter folds the quiet sessions", m.width, m.timeline.on == foldedLane, true))
	}
	// A screen too short for the footer shows the lanes alone.
	if body := height - timelineFooter; body > 2 {
		for len(rows) < body {
			rows = append(rows, "")
		}
		rows = append(rows[:body], m.timelineFooter(t, lanes, i, now)...)
	}
	rows = rows[:min(len(rows), height)]

	if m.timeline.turn != nil {
		box := m.renderTurn(min(m.width-4, 76), height-2)
		rows = ui.Overlay(rows, box, (m.width-ansi.StringWidth(box[0]))/2, max((height-len(box))/2, 0))
	}
	return rows
}

// timelineFooter is what is said under the lanes: what happened in the
// picked cell, how long agents waited for the user, and the legend.
func (m *Model) timelineFooter(t ui.Timeline, lanes []lane, i int, now time.Time) []string {
	picked := ui.Dim.Render("  h/l picks what you did on a lane · d shows the turn")
	if i >= 0 && i < len(lanes) && !m.timeline.at.IsZero() {
		cell := t.CellAt(m.timeline.at)
		var said []string
		for _, mk := range lanes[i].marks {
			if t.CellAt(mk.at) == cell {
				said = append(said, mk.at.Format("15:04")+" "+string(mk.glyph)+" "+mk.text)
			}
		}
		picked = "  " + lanes[i].name + ": " + strings.Join(said, " · ")
	}

	var waited time.Duration
	end := t.CellTime(t.Cells())
	for _, l := range lanes {
		for _, p := range l.ui.Periods {
			if p.State != ui.StateWaiting {
				continue
			}
			to := p.To
			if to.IsZero() {
				to = now
			}
			from, to := maxTime(p.From, t.From), minTime(to, end)
			if to.After(from) {
				waited += to.Sub(from)
			}
		}
	}
	var sums []string
	if waited >= time.Minute {
		sums = append(sums, "agents waited for you "+span(waited))
	}
	var need, notes, queued int
	for _, s := range m.sessions {
		if s.Status == agent.NeedsInput {
			need++
		}
		notes += len(s.Notes)
		queued += len(s.Queue)
	}
	if need > 0 {
		sums = append(sums, fmt.Sprintf("%d wait now", need))
	}
	if notes > 0 {
		sums = append(sums, fmt.Sprintf("%d note(s) not sent", notes))
	}
	if queued > 0 {
		sums = append(sums, fmt.Sprintf("%d queued", queued))
	}
	summary := "  " + strings.Join(sums, " · ")
	if len(sums) == 0 {
		summary = "  nothing is left for you"
	}
	legend := "  █ working ▲ waits for you ─ idle ✓ done ✕ failed │ ↵ answered m message t notes k ask x stop o worktree ◆ card ● commit + several"
	return []string{
		ui.Fit(picked, m.width),
		ui.Fit(summary, m.width),
		ui.Dim.Render(ui.Fit(legend, m.width)),
	}
}

// renderTimelineList draws a narrow timeline as a list of what the user
// did, the newest first.
func (m *Model) renderTimelineList(lanes []lane, height int) []string {
	type line struct {
		at   time.Time
		text string
	}
	var lines []line
	for _, l := range lanes {
		for _, mk := range l.marks {
			lines = append(lines, line{mk.at, mk.at.Format("15:04") + " " + string(mk.glyph) + " " + l.name + ": " + mk.text})
		}
	}
	slices.SortStableFunc(lines, func(a, b line) int { return b.at.Compare(a.at) })
	var rows []string
	for _, l := range lines {
		rows = append(rows, ui.Fit(" "+l.text, m.width))
	}
	if len(rows) == 0 {
		rows = append(rows, ui.Dim.Render(ui.Fit(" nothing you did in this time", m.width)))
	}
	return ui.Block(rows, m.width, height)
}

// timelineHint is what the status bar says the keys of the timeline do.
func (m *Model) timelineHint() string {
	if m.timeline.turn != nil {
		return "j/k move · enter open · esc back"
	}
	return "j/k lane · h/l what you did · d turn · enter go · +/- zoom · H/L scroll · s since you left · q back"
}

// span says how long a time is, to the minute, as "1h20m".
func span(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// cellName says how long a cell lasts, as "5m" or "1h".
func cellName(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
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
