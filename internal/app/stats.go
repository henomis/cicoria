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
	"cmp"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

const (
	// statsLoad is how far back Stats reads the history file.
	statsLoad = 30 * 24 * time.Hour
	// statsWide is the width from which Stats shows two columns.
	statsWide = 100
	// statsFiles is how many of the files changed most it lists.
	statsFiles = 5
	// statsBar is the longest a bar is drawn.
	statsBar = 40
	// idleMax is the longest an agent is taken to wait for your next message
	// after a turn: a longer pause is a break, not a wait.
	idleMax = 4 * time.Hour
)

// statsRanges are the times Stats can show, in days back from today: 1 is
// today only.
var statsRanges = []int{1, 7, 30}

// stats is what the Stats view shows.
type stats struct {
	// back is what it was opened from.
	back mode
	// rng is the time shown, an index of statsRanges.
	rng int
	// top is the first row on screen.
	top int
	// file is the history read from the file, and loading says it is being
	// read.
	file    []session.Event
	loading bool
	// numbers shows the stats as numbers instead of charts.
	numbers bool
}

// statsMsg carries the history read from the file.
type statsMsg struct {
	events []session.Event
	err    error
}

// summary is what Stats counts in the history, from a time to now.
type summary struct {
	turns, typed, sent, interrupted, failed int
	// lengths are how long the turns that ended took, the shortest first,
	// and waits how long agents waited for you each time.
	lengths, waits []time.Duration
	// hours and days count the turns started at each hour of the day and on
	// each day of the week, Monday first.
	hours [24]int
	days  [7]int
	// grid counts them by day of the week and hour.
	grid [7][24]int
	// typedBy and sentBy count the turns typed and sent in each cell of the
	// sparklines, and work is when each agent worked, for the time from
	// from to now.
	typedBy, sentBy []int
	work            [][2]time.Time
	from, now       time.Time
	// idle are how long agents that ended a turn waited for the next one,
	// and together how long one, two, three, four or more worked at once.
	idle     []time.Duration
	together [4]time.Duration
	// buckets is how many cells a session's sparkline has: hours of today,
	// or days.
	buckets  int
	sessions []sessionStats
	agents   []agentStats
	files    []fileCount
}

// agentStats is what Stats counts for the sessions of one agent.
type agentStats struct {
	name              string
	sessions, turns   int
	interrupted, asks int
	lengths           []time.Duration
	waited            time.Duration
}

// who is what Stats knows of the sessions besides their events: their name,
// their agent, whether they are closed, and which are shells, left out.
type who struct {
	name, agent func(id string) string
	closed      func(id string) bool
	shells      []string
}

// notRecorded is the agent of a session the history does not say.
const notRecorded = "not recorded"

// sessionStats is what Stats counts for one session.
type sessionStats struct {
	id, name     string
	turns, files int
	busy, waited time.Duration
	closed       bool
}

type fileCount struct {
	path string
	n    int
}

// openStats shows Stats, or goes back from it.
func (m *Model) openStats() tea.Cmd {
	if m.mode == modeStats {
		m.closeStats()
		return nil
	}
	m.stats.back = modeWorkspace
	if m.mode == modeBoard || m.commandFrom == modeBoard {
		m.stats.back = modeBoard
	}
	m.stats.rng, m.stats.top = 1, 0
	m.mode = modeStats
	return m.loadStats()
}

// closeStats goes back to what Stats was opened from.
func (m *Model) closeStats() { m.mode = m.stats.back }

// loadStats reads, in the background, the history file of the workspace
// further back than the history in memory goes.
func (m *Model) loadStats() tea.Cmd {
	if m.stats.loading {
		return nil
	}
	m.stats.loading = true
	path := session.EventsPath(m.dir)
	since := time.Now().Add(-statsLoad)
	return func() tea.Msg {
		events, err := session.LoadEvents(path, since, historyKeep)
		return statsMsg{events, err}
	}
}

// statsEvents returns the history Stats counts: the file, up to where the
// history in memory starts, then the history in memory, which also has
// what is not written yet.
func (m *Model) statsEvents() []session.Event {
	mem := m.history.Events()
	if len(mem) == 0 {
		return m.stats.file
	}
	older := m.stats.file[:0:0]
	for _, e := range m.stats.file {
		if e.T.Before(mem[0].T) {
			older = append(older, e)
		}
	}
	return append(older, mem...)
}

// statsFrom returns when the time Stats shows starts: midnight, days back.
func statsFrom(rng int, now time.Time) time.Time {
	y, mo, d := now.Date()
	return time.Date(y, mo, d-statsRanges[rng]+1, 0, 0, 0, 0, now.Location())
}

// summarize counts what happened in the history from from to now.
func summarize(events []session.Event, from, now time.Time, days int, w who) summary {
	sum := summary{buckets: days, from: from, now: now}
	if days == 1 {
		sum.buckets = 24
	}
	sum.typedBy, sum.sentBy = make([]int, sum.buckets), make([]int, sum.buckets)
	bucket := func(t time.Time) int {
		if days == 1 {
			return t.Hour()
		}
		y, mo, d := t.Date()
		return int(time.Date(y, mo, d, 0, 0, 0, 0, t.Location()).Sub(from).Hours()+12) / 24
	}

	byID := map[string][]session.Event{}
	for _, e := range events {
		if e.S != "" && !slices.Contains(w.shells, e.S) {
			byID[e.S] = append(byID[e.S], e)
		}
	}
	ends := runEnds(events)
	files := map[string]int{}
	agents := map[string]*agentStats{}
	var work [][2]time.Time

	for id, evs := range byID {
		ss := sessionStats{id: id, name: w.name(id), closed: w.closed(id)}
		touched := map[string]bool{}
		var start, ended time.Time
		var lengths []time.Duration
		interrupted, asks := 0, 0
		for _, e := range evs {
			if e.T.Before(from) || e.T.After(now) {
				continue
			}
			switch e.K {
			case session.KindTurnStart:
				if g := e.T.Sub(ended); !ended.IsZero() && g <= idleMax && !endedBetween(ends, ended, e.T) {
					sum.idle = append(sum.idle, g)
				}
				start, ended = e.T, time.Time{}
				ss.turns++
				sum.turns++
				b := bucket(e.T)
				in := b >= 0 && b < sum.buckets
				if e.V == "typed" {
					sum.typed++
					if in {
						sum.typedBy[b]++
					}
				} else {
					sum.sent++
					if in {
						sum.sentBy[b]++
					}
				}
				day := (int(e.T.Weekday()) + 6) % 7
				sum.hours[e.T.Hour()]++
				sum.days[day]++
				sum.grid[day][e.T.Hour()]++
			case session.KindTurnEnd, session.KindTurnFailed, session.KindInterrupted:
				if !start.IsZero() {
					lengths = append(lengths, e.T.Sub(start))
					start = time.Time{}
				}
				ended = e.T
				switch e.K {
				case session.KindTurnFailed:
					sum.failed++
				case session.KindInterrupted:
					sum.interrupted++
					interrupted++
				}
				for _, f := range e.Files {
					files[f]++
					touched[f] = true
				}
			}
		}
		for _, p := range periods(evs, ends) {
			to := p.To
			if to.IsZero() {
				to = now
			}
			span := minTime(to, now).Sub(maxTime(p.From, from))
			if span <= 0 {
				continue
			}
			switch p.State {
			case ui.StateWorking:
				ss.busy += span
				work = append(work, [2]time.Time{maxTime(p.From, from), minTime(to, now)})
			case ui.StateWaiting:
				ss.waited += span
				sum.waits = append(sum.waits, span)
				asks++
			}
		}
		ss.files = len(touched)
		// A session that only flickered, as a shell once did, is left out.
		if ss.turns == 0 && ss.busy < time.Minute && ss.waited == 0 {
			continue
		}
		sum.sessions = append(sum.sessions, ss)
		sum.lengths = append(sum.lengths, lengths...)
		name := cmp.Or(w.agent(id), notRecorded)
		a := agents[name]
		if a == nil {
			a = &agentStats{name: name}
			agents[name] = a
		}
		a.sessions++
		a.turns += ss.turns
		a.interrupted += interrupted
		a.asks += asks
		a.waited += ss.waited
		a.lengths = append(a.lengths, lengths...)
	}
	sum.together, sum.work = together(work), work
	for _, a := range agents {
		slices.Sort(a.lengths)
		sum.agents = append(sum.agents, *a)
	}
	slices.SortFunc(sum.agents, func(a, b agentStats) int {
		return cmp.Or(cmp.Compare(b.turns, a.turns), strings.Compare(a.name, b.name))
	})
	slices.Sort(sum.idle)
	slices.Sort(sum.lengths)
	slices.Sort(sum.waits)
	slices.SortFunc(sum.sessions, func(a, b sessionStats) int {
		return cmp.Or(cmp.Compare(b.turns, a.turns), cmp.Compare(b.busy, a.busy), strings.Compare(a.name, b.name))
	})
	for f, n := range files {
		sum.files = append(sum.files, fileCount{f, n})
	}
	slices.SortFunc(sum.files, func(a, b fileCount) int { return cmp.Or(cmp.Compare(b.n, a.n), strings.Compare(a.path, b.path)) })
	if len(sum.files) > statsFiles {
		sum.files = sum.files[:statsFiles]
	}
	return sum
}

// endedBetween reports whether a run of cicoria ended between two times.
func endedBetween(ends []time.Time, from, to time.Time) bool {
	return slices.ContainsFunc(ends, func(t time.Time) bool { return !t.Before(from) && t.Before(to) })
}

// together returns how long one, two, three, and four or more of the spans
// overlapped.
func together(spans [][2]time.Time) [4]time.Duration {
	type edge struct {
		at time.Time
		d  int
	}
	var edges []edge
	for _, sp := range spans {
		if sp[1].After(sp[0]) {
			edges = append(edges, edge{sp[0], 1}, edge{sp[1], -1})
		}
	}
	// At the same time, a span that ends goes before one that starts.
	slices.SortFunc(edges, func(a, b edge) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.d, b.d)) })
	var out [4]time.Duration
	n := 0
	for k, e := range edges {
		if k > 0 && n > 0 {
			out[min(n, 4)-1] += e.at.Sub(edges[k-1].at)
		}
		n += e.d
	}
	return out
}

// quantile returns the value under which a share q of the sorted values
// are, by nearest rank.
func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(q*float64(len(sorted)-1)+0.5)]
}

// summaryNow counts the history for the time Stats shows.
func (m *Model) summaryNow(now time.Time) summary {
	events := m.statsEvents()
	names := m.sessionNames(events)
	var shells []string
	for _, s := range m.sessions {
		if isShell(s.Adapter) {
			shells = append(shells, s.ID)
		}
	}
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return "a closed session"
	}
	// The agent a session opened with, or the one it runs now.
	agents := map[string]string{}
	for _, e := range events {
		if e.K == session.KindSessionOpen && e.Agent != "" {
			agents[e.S] = e.Agent
		}
	}
	for _, s := range m.sessions {
		agents[s.ID] = s.Adapter.Name()
	}
	w := who{
		name:   name,
		agent:  func(id string) string { return agents[id] },
		closed: func(id string) bool { s, _ := m.byID(id); return s == nil },
		shells: shells,
	}
	return summarize(events, statsFrom(m.stats.rng, now), now, statsRanges[m.stats.rng], w)
}

// statsKey handles the keys of Stats.
func (m *Model) statsKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if m.prefix {
		m.prefix = false
		// The key that opened Stats closes it.
		if key == "i" {
			m.closeStats()
			return nil
		}
		return m.runPrefix(k)
	}
	if key == m.cfg.Prefix {
		m.prefix = true
		return nil
	}
	// The rows past the end are cut when Stats is drawn.
	page := max(m.height-2, 1)
	switch key {
	case "esc", "q":
		m.closeStats()
	case "h", "left":
		m.stats.rng = max(m.stats.rng-1, 0)
	case "l", "right":
		m.stats.rng = min(m.stats.rng+1, len(statsRanges)-1)
	case "j", "down":
		m.stats.top++
	case "k", "up":
		m.stats.top = max(m.stats.top-1, 0)
	case "space", " ", "f", "pgdown":
		m.stats.top += page
	case "b", "pgup":
		m.stats.top = max(m.stats.top-page, 0)
	case "g", "home":
		m.stats.top = 0
	case "G", "end":
		m.stats.top = math.MaxInt / 2
	case "t":
		m.stats.numbers = !m.stats.numbers
	}
	return nil
}

// statsHint is what the status bar says the keys of Stats do.
func (m *Model) statsHint() string {
	view := "t numbers"
	if m.stats.numbers {
		view = "t charts"
	}
	return "h/l time · j/k scroll · " + view + " · q back"
}

// renderStats draws Stats over the whole screen but the status bar.
func (m *Model) renderStats() []string {
	height := max(m.height-1, 2)
	now := time.Now()
	sum := m.summaryNow(now)
	w := max(m.width, 0)

	title := fmt.Sprintf(" stats  %s · %s · %d turn(s) · %d session(s)", filepath.Base(m.dir), statsRangeName(m.stats.rng), sum.turns, len(sum.sessions))
	if m.stats.loading {
		title += " · reading the history…"
	}
	head := []string{ui.Reverse.Render(ui.Fit(title, w))}

	if sum.turns == 0 && len(sum.sessions) == 0 {
		body := []string{"", ui.Dim.Render(ui.Fit("  No turns in "+statsRangeName(m.stats.rng)+". l shows a longer time.", w))}
		return ui.Block(append(head, body...), w, height)
	}

	colW := w
	if w >= statsWide {
		colW = w / 2
	}
	// A terminal too narrow for charts shows numbers.
	charts := !m.stats.numbers && w >= timelineNarrow
	left := slices.Concat(statsHours(sum, colW), []string{""}, statsDays(sum, colW))
	right := slices.Concat(statsStarts(sum, colW), []string{"", section("time", colW)}, statsLengths(sum), statsWaits(sum), statsIdle(sum), statsTogether(sum))
	if charts {
		left = slices.Concat(graphWhen(sum, colW), []string{""}, graphStarts(sum, colW), []string{""}, graphWaits(sum, colW))
		rw := w - colW
		if w < statsWide {
			rw = w
		}
		right = slices.Concat(graphTurns(sum, rw), []string{""}, graphLengths(sum, rw), []string{""}, graphTogether(sum, rw))
	}
	var top []string
	if w >= statsWide {
		h := max(len(left), len(right))
		top = ui.JoinColumns(ui.Block(left, colW, h), ui.Block(right, w-colW, h))
	} else {
		top = slices.Concat(left, []string{""}, right)
	}

	rows := top
	if len(sum.agents) > 0 {
		rows = append(rows, "", section("agents", w))
		if charts {
			rows = append(rows, graphAgents(sum, w)...)
		} else {
			rows = append(rows, statsAgents(sum, w)...)
		}
	}
	if len(sum.files) > 0 {
		rows = append(rows, "", section("files the agents changed most", w))
		if charts {
			rows = append(rows, graphFiles(sum, w)...)
		}
		for _, f := range sum.files {
			if charts {
				break
			}
			rows = append(rows, ui.Fit(fmt.Sprintf("  %3d  %s", f.n, f.path), w))
		}
	}

	room := height - len(head)
	m.stats.top = max(min(m.stats.top, len(rows)-room), 0)
	rows = rows[m.stats.top:]
	return ui.Block(append(head, rows...), w, height)
}

// statsRangeName says what time a range of Stats shows.
func statsRangeName(rng int) string {
	if statsRanges[rng] == 1 {
		return "today"
	}
	return fmt.Sprintf("the last %d days", statsRanges[rng])
}

// section is the title of a part of Stats, on a rule.
func section(title string, w int) string {
	t := "─ " + title + " "
	return ui.Dim.Render(t + strings.Repeat("─", max(w-ansi.StringWidth(t), 0)))
}

// bar is a horizontal bar of n out of most, at most w cells.
func bar(n, most, w int) string {
	if most <= 0 || w <= 0 || n <= 0 {
		return ""
	}
	w = min(w, statsBar)
	return strings.Repeat("█", max(n*w/most, 1))
}

// statsHours draws the turns by hour of the day, from the first hour with
// one to the last.
func statsHours(sum summary, w int) []string {
	out := []string{section("turns by hour", w)}
	lo, hi := -1, -1
	most := 0
	for h, n := range sum.hours {
		if n > 0 {
			if lo < 0 {
				lo = h
			}
			hi = h
			most = max(most, n)
		}
	}
	if lo < 0 {
		return append(out, ui.Dim.Render("  none"))
	}
	barW := w - 12
	if w < timelineNarrow {
		barW = 0
	}
	for h := lo; h <= hi; h++ {
		out = append(out, fmt.Sprintf("  %02d %s %s", h, ui.Accent.Render(bar(sum.hours[h], most, barW)), ui.Dim.Render(fmt.Sprint(sum.hours[h]))))
	}
	return out
}

// statsDays draws the turns by day of the week.
func statsDays(sum summary, w int) []string {
	out := []string{section("turns by day", w)}
	most := slices.Max(sum.days[:])
	barW := w - 13
	if w < timelineNarrow {
		barW = 0
	}
	for d, name := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		out = append(out, fmt.Sprintf("  %s %s %s", name, ui.Accent.Render(bar(sum.days[d], most, barW)), ui.Dim.Render(fmt.Sprint(sum.days[d]))))
	}
	return out
}

// statsStarts says how the turns started, and how many were cut short.
func statsStarts(sum summary, w int) []string {
	out := []string{section("how turns start", w)}
	barW := w - 37
	if w < timelineNarrow {
		barW = 0
	}
	line := func(label string, n int, style func(...string) string) string {
		share := 0
		if sum.turns > 0 {
			share = (n*100 + sum.turns/2) / sum.turns
		}
		return fmt.Sprintf("  %-22s %4d %3d%% %s", label, n, share, style(bar(n, sum.turns, barW)))
	}
	return append(out,
		line("typed by you", sum.typed, ui.Accent.Render),
		line("sent by cicoria", sum.sent, ui.Accent.Render),
		line("interrupted", sum.interrupted, ui.Warn.Render),
		line("failed", sum.failed, ui.Bad.Render),
	)
}

// statsLengths says how long the turns took.
func statsLengths(sum summary) []string {
	if len(sum.lengths) == 0 {
		return []string{ui.Dim.Render("  no turn ended")}
	}
	l := sum.lengths
	return []string{
		fmt.Sprintf("  turns took      median %s · 3 in 4 under %s · longest %s", span(quantile(l, 0.5)), span(quantile(l, 0.75)), span(l[len(l)-1])),
	}
}

// statsWaits says how long agents waited for you.
func statsWaits(sum summary) []string {
	if len(sum.waits) == 0 {
		return []string{ui.Dim.Render("  no agent waited for you")}
	}
	var total time.Duration
	for _, d := range sum.waits {
		total += d
	}
	return []string{
		fmt.Sprintf("  waited on you   %d time(s) · median %s · %s in all", len(sum.waits), span(quantile(sum.waits, 0.5)), ui.Warn.Render(span(total))),
	}
}

// statsIdle says how long agents that ended a turn waited for the next.
func statsIdle(sum summary) []string {
	if len(sum.idle) == 0 {
		return nil
	}
	return []string{
		fmt.Sprintf("  idle after done %d time(s) · median %s · 3 in 4 under %s", len(sum.idle), span(quantile(sum.idle, 0.5)), span(quantile(sum.idle, 0.75))),
	}
}

// statsTogether says how long several agents worked at once.
func statsTogether(sum summary) []string {
	var parts []string
	for n, d := range sum.together {
		if d >= time.Minute {
			label := fmt.Sprint(n + 1)
			if n == len(sum.together)-1 {
				label += "+"
			}
			parts = append(parts, label+" "+span(d))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []string{"  agents at once  " + strings.Join(parts, " · ")}
}

// statsAgents compares the agents the sessions ran, the busiest first.
func statsAgents(sum summary, w int) []string {
	nameW := 5
	for _, a := range sum.agents {
		nameW = max(nameW, min(ansi.StringWidth(a.name), 16))
	}
	var out []string
	for _, a := range sum.agents {
		share := 0
		if a.turns > 0 {
			share = (a.interrupted*100 + a.turns/2) / a.turns
		}
		row := fmt.Sprintf("   %s %4d turn(s) in %d session(s) · median %-5s · %3d%% interrupted · asked you %d time(s) · waited %s",
			ui.Fit(a.name, nameW), a.turns, a.sessions, orDash(quantile(a.lengths, 0.5)), share, a.asks, orDash(a.waited))
		if a.name == notRecorded {
			row = ui.Dim.Render(row)
		}
		out = append(out, ui.Fit(row, w))
	}
	return out
}

// orDash says how long a time is, or "–" for none.
func orDash(d time.Duration) string {
	if d <= 0 {
		return "–"
	}
	return span(d)
}
