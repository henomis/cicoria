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
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

const (
	// timelineNarrow is the width under which the timeline is the log of
	// what you did alone, without the bars.
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
	// sel is the entry of the log under the cursor, and top the first one
	// on screen.
	sel entryKey
	top int
	// turn is the turn shown over the timeline, if any.
	turn *turnView
	// frame is how the bars are drawn: kept while it fits, so that they
	// do not shift as time passes.
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

// entry is one line of the log of what you did.
type entry struct {
	at       time.Time
	id, name string
	text     string
	// bad marks what went wrong, as a turn that failed.
	bad bool
}

// entryKey says which entry of the log is under the cursor, wherever new
// ones push it.
type entryKey struct {
	at time.Time
	id string
}

func (e entry) key() entryKey { return entryKey{e.at, e.id} }

// openTimeline shows the timeline, or goes back from it.
func (m *Model) openTimeline() tea.Cmd {
	if m.mode == modeTimeline {
		m.closeTimeline()
		return nil
	}
	return m.openTimelineOn(nil)
}

// openTimelineOn shows the timeline, with the cursor on the last thing done
// in a session, when one is given.
func (m *Model) openTimelineOn(s *session.Session) tea.Cmd {
	m.timeline.back = modeWorkspace
	if m.mode == modeBoard {
		m.timeline.back = modeBoard
	}
	m.timeline.rng, m.timeline.sel, m.timeline.top, m.timeline.turn = sinceAway, entryKey{}, 0, nil
	m.timeline.frame = barFrame{}
	if s != nil {
		_, _, entries := m.timelineData(time.Now())
		if i := slices.IndexFunc(entries, func(e entry) bool { return e.id == s.ID }); i >= 0 {
			m.timeline.sel = entries[i].key()
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

// sentFrom names where a message came from, as the timeline says it.
func sentFrom(from string) string {
	switch from {
	case fromNotes:
		return "review notes"
	case fromCard:
		return "the card's prompt"
	case fromAsk:
		return "a message from another session"
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

// say returns what the log says of event k of a lane, if it is something
// you did, or something that went wrong.
func (l lane) say(k int, name func(id string) string, now time.Time) (string, bool) {
	e := l.events[k]
	web := ""
	if e.Src == session.FromWeb {
		web = " from the web"
	}
	switch e.K {
	case session.KindAnswer:
		text := "answered"
		if e.V != "" {
			text += " “" + e.V + "”"
		}
		text += web
		// How long the agent waited for it.
		for j := k - 1; j >= 0; j-- {
			if p := l.events[j]; p.K == session.KindStatus {
				if p.V == "needs input" && e.T.Sub(p.T) >= time.Minute {
					text += " · it waited " + span(e.T.Sub(p.T))
				}
				break
			}
		}
		return text, true
	case session.KindPromptTyped:
		if e.X == "" {
			return "typed a prompt" + l.outcome(k, now), true
		}
		return "typed" + quoted(e) + l.outcome(k, now), true
	case session.KindQueue, session.KindSend:
		// What the queue sends by itself was said when it was queued.
		if e.K == session.KindSend && e.Src == "" {
			return "", false
		}
		verb := "queued"
		if e.K == session.KindSend {
			verb = "sent"
		}
		// What was sent at once started a turn; what was queued waits.
		then := ""
		if e.K == session.KindSend {
			then = l.outcome(k, now)
		}
		switch e.V {
		case fromNotes:
			return verb + " review notes" + web + then, true
		case fromAsk:
			return "you asked it" + quoted(e) + " from another session", true
		case fromAnswer:
			return "got the answer of another session", true
		case fromCard:
			return "", false
		}
		what := quoted(e)
		if what == "" {
			what = " a message"
		}
		if e.To != "" {
			what += ", for " + name(e.To) + " to answer"
		}
		return verb + what + web + then, true
	case session.KindInterrupted:
		return "stopped the turn" + web, true
	case session.KindUnqueue:
		return "took back" + quoted(e) + web, true
	case session.KindWorktreeAdd:
		return "made the worktree " + tilde(e.V), true
	case session.KindCardStart:
		return "started the card “" + e.V + "”", true
	case session.KindTurnFailed:
		text := "✕ the turn failed"
		for j := k - 1; j >= 0; j-- {
			if p := l.events[j]; p.K == session.KindTurnStart {
				text += " after " + span(e.T.Sub(p.T))
				break
			}
		}
		return text, true
	case session.KindSessionClose:
		return "closed it", true
	}
	return "", false
}

// outcome says what came of the message of event k: how the turn it
// started went, as " → done in 12m, 3 files". It is empty when no turn is
// known to have started for it.
func (l lane) outcome(k int, now time.Time) string {
	e := l.events[k]
	start := -1
	for j := k + 1; j < len(l.events) && l.events[j].T.Sub(e.T) <= fromQueueWithin; j++ {
		f := l.events[j]
		if f.K == session.KindTurnStart {
			start = j
			break
		}
		// Another message came first: the turn would be its own.
		if f.K == session.KindSend || f.K == session.KindPromptTyped {
			return ""
		}
	}
	if start < 0 {
		return ""
	}
	began := l.events[start].T
	for _, f := range l.events[start+1:] {
		took := span(f.T.Sub(began))
		switch f.K {
		case session.KindTurnEnd:
			text := " → done in " + took
			if n := len(f.Files); n == 1 {
				text += ", 1 file"
			} else if n > 1 {
				text += fmt.Sprintf(", %d files", n)
			}
			return text
		case session.KindTurnFailed:
			return " → failed after " + took
		case session.KindInterrupted:
			return " → you stopped it after " + took
		case session.KindTurnStart, session.KindSessionClose:
			return " → ran " + took
		}
	}
	if l.s != nil && (l.s.Status == agent.Working || l.s.Status == agent.NeedsInput) {
		return " → working for " + span(now.Sub(began))
	}
	return ""
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
// with something to show in that time, and the log of what you did then,
// the newest first.
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
		if !slices.Contains(ids, id) && !evs[len(evs)-1].T.Before(from) {
			ids = append(ids, id)
		}
	}

	width := 0
	for _, id := range ids {
		width = max(width, ansi.StringWidth(name(id)))
	}
	t := ui.Timeline{From: from, Now: now, Width: m.width, LabelWidth: min(width, 16) + 4}
	t.From, t.Step = m.layOut(from, now, t.Cells())

	var lanes []lane
	var entries []entry
	for _, id := range ids {
		s, _ := m.byID(id)
		l := lane{id: id, s: s, name: name(id), events: byID[id]}
		l.periods = periods(l.events, runEnds)
		said := 0
		for k, e := range l.events {
			if e.T.Before(from) {
				continue
			}
			if text, ok := l.say(k, name, now); ok {
				entries = append(entries, entry{at: e.T, id: id, name: l.name, text: text, bad: e.K == session.KindTurnFailed})
				said++
			}
		}
		for _, c := range m.timeline.commits[id] {
			if !c.When.Before(from) {
				entries = append(entries, entry{at: c.When, id: id, name: l.name, text: "● commit " + c.Short + " " + c.Subject})
				said++
			}
		}
		// A session that did nothing in that time is left out of the bars.
		busy := s != nil && (s.Status == agent.Working || s.Status == agent.NeedsInput)
		active := slices.ContainsFunc(t.States(l.periods), func(st ui.State) bool { return st > ui.StateIdle })
		if said > 0 || busy || active {
			lanes = append(lanes, l)
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return b.at.Compare(a.at) })
	return t, lanes, entries
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

// cursor returns where the entry under the cursor is in the log, putting
// the cursor on the newest one when it is on none.
func (m *Model) cursor(entries []entry) int {
	i := slices.IndexFunc(entries, func(e entry) bool { return e.key() == m.timeline.sel })
	if i < 0 && len(entries) > 0 {
		i = 0
		m.timeline.sel = entries[0].key()
	}
	return i
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
	_, lanes, entries := m.timelineData(now)
	i := m.cursor(entries)
	move := func(to int) {
		if len(entries) > 0 {
			m.timeline.sel = entries[max(min(to, len(entries)-1), 0)].key()
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
		move(len(entries) - 1)
	case "+", "=":
		m.zoomTimeline(-1, now)
	case "-":
		m.zoomTimeline(1, now)
	case "s":
		m.timeline.rng = sinceAway
	case "a":
		m.timeline.back = modeWorkspace
		m.closeTimeline()
		return m.nextAttention()
	case "enter":
		if i < 0 {
			return nil
		}
		s, j := m.byID(entries[i].id)
		if s == nil {
			m.say(entries[i].name + " is closed")
			return nil
		}
		m.timeline.back = modeWorkspace
		m.closeTimeline()
		return m.switchTo(j)
	case "d", "space", " ":
		if i < 0 {
			return nil
		}
		k := slices.IndexFunc(lanes, func(l lane) bool { return l.id == entries[i].id })
		if k < 0 {
			return nil
		}
		m.timeline.turn = m.turnAt(lanes[k], entries[i].at, now)
		if m.timeline.turn == nil {
			m.say("no turn of " + lanes[k].name + " to show there")
		}
	case "m":
		if i >= 0 {
			if s, _ := m.byID(entries[i].id); s != nil {
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
// bar: the bars of the sessions, the log of what you did, newest first,
// and what is left for you.
func (m *Model) renderTimeline() []string {
	height := max(m.height-1, 2)
	now := time.Now()
	t, lanes, entries := m.timelineData(now)
	i := m.cursor(entries)

	header := " timeline  " + m.timelineTitle(now)
	var waits []string
	for _, l := range lanes {
		if l.waiting() {
			waits = append(waits, l.name+" ▲ waits "+lasting(l.s))
		}
	}
	right := strings.Join(waits, " · ") + " "
	pad := max(m.width-ansi.StringWidth(header)-ansi.StringWidth(right), 1)
	rows := []string{ui.Reverse.Render(ui.Fit(header+strings.Repeat(" ", pad)+right, m.width))}

	if m.width >= timelineNarrow && len(lanes) > 0 {
		bars := make([]ui.Bar, len(lanes))
		labels := make([]*lipgloss.Style, len(lanes))
		for k, l := range lanes {
			label := l.name
			if l.s != nil {
				label = ui.Fit(l.name, t.LabelWidth-4) + " " + statusGlyph(l.s)
			}
			bars[k] = ui.Bar{Label: label, Periods: l.periods, Dim: l.s == nil}
			if i >= 0 && l.id == entries[i].id {
				labels[k] = &ui.Bold
			}
		}
		// The bars take at most half of the screen.
		bars = bars[:min(len(bars), max((height-6)/2, 1))]
		rows = append(rows, t.Axis())
		rows = append(rows, t.Render(bars, labels)...)
		var at time.Time
		if i >= 0 {
			at = entries[i].at
		}
		rows = append(rows, t.Pointer(at))
	}
	rule := ui.Dim.Render(strings.Repeat("─", max(m.width, 0)))
	rows = append(rows, rule)

	room := max(height-len(rows)-2, 1)
	var log []string
	if len(entries) == 0 {
		log = append(log, ui.Dim.Render(ui.Fit("  you did nothing here in "+m.timelineTitle(now)+": - shows a longer time", m.width)))
	}
	nameW := 0
	for _, e := range entries {
		nameW = max(nameW, min(ansi.StringWidth(e.name), 16))
	}
	m.timeline.top = ui.Window(max(i, 0), m.timeline.top, room, len(entries))
	for k := m.timeline.top; k < min(len(entries), m.timeline.top+room); k++ {
		e := entries[k]
		when := e.at.Format("15:04")
		if now.Sub(e.at) > 20*time.Hour {
			when = e.at.Format("Mon 15:04")
		}
		text := e.text
		if e.bad && k != i {
			text = ui.Bad.Render(text)
		}
		lead := " "
		if k == i {
			lead = "▸"
		}
		log = append(log, ui.Selected(lead+when+" "+ui.Fit(e.name, nameW)+"  "+text, m.width, k == i, true))
	}
	rows = append(rows, ui.Block(log, m.width, room)...)
	rows = append(rows, rule, ui.Fit(m.timelineSummary(t, lanes, now), m.width))
	rows = rows[:min(len(rows), height)]

	if m.timeline.turn != nil {
		box := m.renderTurn(min(m.width-4, 76), height-2)
		rows = ui.Overlay(rows, box, (m.width-ansi.StringWidth(box[0]))/2, max((height-len(box))/2, 0))
	}
	return rows
}

// timelineSummary says how long agents waited for you in the time shown,
// and what is left for you to do.
func (m *Model) timelineSummary(t ui.Timeline, lanes []lane, now time.Time) string {
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
		sums = append(sums, fmt.Sprintf("%d message(s) queued", queued))
	}
	legend := ui.Dim.Render(" · █ working ▲ waits for you ─ idle ✓ done ✕ failed")
	if len(sums) == 0 {
		return ui.Dim.Render(" nothing is left for you") + legend
	}
	return " " + strings.Join(sums, " · ") + legend
}

// statusGlyph is the status mark of a session without its colour.
func statusGlyph(s *session.Session) string { return ansi.Strip(badge(s)) }

// timelineHint is what the status bar says the keys of the timeline do.
func (m *Model) timelineHint() string {
	if m.timeline.turn != nil {
		return "j/k move · enter open · esc back"
	}
	return "j/k move · d turn · enter go · m queue · +/- time · s since you left · q back"
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
