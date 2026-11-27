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
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// What the brain fry meter takes as a risk.
// shortcut: guessed from research (BCG 2026: output falls from the 4th
// agent; breaks under 10 minutes do not restore attention; a 3-hour cap on
// AI-assisted coding), tune them from real ledgers.
const (
	// breakMin is the shortest pause in what the user does that is a break.
	breakMin = 15 * time.Minute
	// stretchRaised and stretchHigh are how long without a break raise the
	// risk.
	stretchRaised = 90 * time.Minute
	stretchHigh   = 3 * time.Hour
	// crowdAgents is how many agents at once are a crowd, and crowdRaised
	// and crowdHigh how long of the last hour they worked as one.
	crowdAgents = 4
	crowdRaised = 10 * time.Minute
	crowdHigh   = 30 * time.Minute
	// switchesRaised and switchesHigh are how many changes of session in
	// the last hour raise the risk.
	switchesRaised = 20
	switchesHigh   = 40
	// unreadRaised and unreadHigh are how many files changed by agents and
	// not opened raise the risk.
	unreadRaised = 10
	unreadHigh   = 30
	// activeRaised and activeHigh are how long of acting in the last day
	// raise the risk, pauses of breakMin or more left out.
	activeRaised = 4 * time.Hour
	activeHigh   = 6 * time.Hour
	// meterTicks is how many ticks pass between looks at the risk, and
	// meterBarMin the shortest bar the meter keeps when it names a rule.
	meterTicks  = 4
	meterBarMin = 4
	// healthTicks is how many ticks pass between looks at the history the
	// health view counts.
	healthTicks = 60
)

// level is how high the risk of brain fry is.
type level int

const (
	levelLow level = iota
	levelRaised
	levelHigh
)

func (l level) String() string {
	return [...]string{"low", "raised", "high"}[l]
}

// render colours a text as the level.
func (l level) render(s string) string {
	return [...]func(...string) string{ui.Good.Render, ui.Warn.Render, ui.Bad.Render}[l](s)
}

// rule is one of the reasons the risk can rise, with where it stands now.
type rule struct {
	lvl level
	// label names it, and short is the name the meter has room for.
	label, short string
	// val is its value now, and raised and high are where those levels
	// start: minutes when dur is set, else a count.
	val, raised, high int
	dur               bool
	// hint says what lowers it.
	hint string
}

// of says a value of the rule, as a time or a count.
func (r rule) of(n int) string {
	if r.dur {
		return orDash(time.Duration(n) * time.Minute)
	}
	return fmt.Sprint(n)
}

// next returns the level the rule rises to next and where, false at high.
func (r rule) next() (level, int, bool) {
	switch r.lvl {
	case levelHigh:
		return 0, 0, false
	case levelRaised:
		return levelHigh, r.high, true
	}
	return levelRaised, r.raised, true
}

// near reports whether a rule still low is three quarters of the way to
// raised.
// shortcut: three quarters is a guess, like the thresholds.
func (r rule) near() bool {
	return r.lvl == levelLow && r.val*4 >= r.raised*3
}

// live is what the rules know of the workspace besides its history.
type live struct {
	// active is how long the user has acted in cicoria without a break,
	// 0 when they are on one.
	active time.Duration
	// working is how many agents work now, unread how many files they
	// changed that the user has not opened.
	working, unread int
	shells          []string
}

// levelOf is the highest level of the rules.
func levelOf(rules []rule) level {
	l := levelLow
	for _, r := range rules {
		l = max(l, r.lvl)
	}
	return l
}

// top returns the first of the rules at the highest level.
func top(rules []rule) (rule, bool) {
	l := levelOf(rules)
	if l == levelLow {
		return rule{}, false
	}
	for _, r := range rules {
		if r.lvl == l {
			return r, true
		}
	}
	return rule{}, false
}

// healthRules returns every rule, in the same order, with its level now.
func healthRules(events []session.Event, in live, now time.Time) []rule {
	events = eventsSince(events, now.Add(-24*time.Hour))
	minutes := func(d time.Duration) int { return int(d / time.Minute) }
	at := func(r rule) rule {
		switch {
		case r.val >= r.high:
			r.lvl = levelHigh
		case r.val >= r.raised:
			r.lvl = levelRaised
		}
		return r
	}

	// The stretch on now is known from the keys: what the history has up to
	// its start, then the whole of it.
	since := now.Add(-in.active)
	_, active, _, _ := stretches(humanActs(events, now.Add(-24*time.Hour), since), since)
	active += in.active

	hour := now.Add(-time.Hour)
	crowd := summarize(events, hour, now, 1, quietWho(in.shells)).together[crowdAgents-1]
	switches := 0
	for _, e := range events {
		if e.K == session.KindSwitch && e.T.After(hour) && !e.T.After(now) {
			switches++
		}
	}

	crowdRule := at(rule{label: fmt.Sprintf("%d+ agents at once, last hour", crowdAgents), short: fmt.Sprintf("%d+ agents, 1 h", crowdAgents),
		val: minutes(crowd), raised: minutes(crowdRaised), high: minutes(crowdHigh), dur: true,
		hint: fmt.Sprintf("%d working now; fewer agents at once lowers it", in.working)})
	if in.working >= crowdAgents && crowdRule.lvl == levelLow {
		crowdRule.lvl = levelRaised
	}
	return []rule{
		at(rule{label: "acting without a 15-minute pause", short: "no 15-min pause",
			val: minutes(in.active), raised: minutes(stretchRaised), high: minutes(stretchHigh), dur: true,
			hint: "a 15-minute pause ends it; z holds everything"}),
		at(rule{label: "active in the last 24 hours", short: "active, 24 h",
			val: minutes(active), raised: minutes(activeRaised), high: minutes(activeHigh), dur: true,
			hint: "pauses left out; a pause stops it growing"}),
		crowdRule,
		at(rule{label: "changes of session, last hour", short: "switches, 1 h",
			val: switches, raised: switchesRaised, high: switchesHigh,
			hint: "stay longer on one session"}),
		at(rule{label: "changed files not opened", short: "not opened",
			val: in.unread, raised: unreadRaised, high: unreadHigh,
			hint: "open their diffs in the changes panel, listed below"}),
	}
}

// eventsSince returns the events, in order of time, from a time on, after
// what the periods of work need from before it: the last status of each
// session, or its close, which ends its period, and the runs of cicoria.
func eventsSince(events []session.Event, from time.Time) []session.Event {
	i, _ := slices.BinarySearchFunc(events, from, func(e session.Event, t time.Time) int { return e.T.Compare(t) })
	var before []session.Event
	seen := map[string]bool{}
	for k := i - 1; k >= 0; k-- {
		switch e := events[k]; {
		case e.K == session.KindRunStart || e.K == session.KindRunEnd:
			before = append(before, e)
		case (e.K == session.KindStatus || e.K == session.KindSessionClose) && !seen[e.S]:
			seen[e.S] = true
			before = append(before, e)
		}
	}
	slices.Reverse(before)
	return append(before, events[i:]...)
}

// quietWho is what summarize needs to count work, without names.
func quietWho(shells []string) who {
	return who{
		name:   func(string) string { return "" },
		agent:  func(string) string { return "" },
		closed: func(string) bool { return false },
		shells: shells,
	}
}

// humanAct reports whether an event is something the user did.
func humanAct(e session.Event) bool {
	switch e.K {
	case session.KindPromptTyped, session.KindAnswer, session.KindCardStart, session.KindSwitch:
		return true
	case session.KindSend, session.KindQueue:
		return e.Src == session.FromTUI || e.Src == session.FromWeb
	}
	return false
}

// humanActs returns when the user did something, from from to now, in
// order.
func humanActs(events []session.Event, from, now time.Time) []time.Time {
	var acts []time.Time
	for _, e := range events {
		if humanAct(e) && !e.T.Before(from) && !e.T.After(now) {
			acts = append(acts, e.T)
		}
	}
	slices.SortFunc(acts, time.Time.Compare)
	return acts
}

// stretches returns the longest time the user acted without a break of
// breakMin, the time of all those stretches together, the one still going
// at now (0 when they are on a break), and how many breaks they took
// between their acts.
// shortcut: only the acts the history records count, and the keys of the
// stretch on now: acting with keys alone in a past stretch, for over
// breakMin between two acts, looks like a break.
func stretches(acts []time.Time, now time.Time) (longest, total, last time.Duration, breaks int) {
	if len(acts) == 0 {
		return 0, 0, 0, 0
	}
	start := acts[0]
	for i := 1; i < len(acts); i++ {
		if acts[i].Sub(acts[i-1]) >= breakMin {
			longest = max(longest, acts[i-1].Sub(start))
			total += acts[i-1].Sub(start)
			breaks++
			start = acts[i]
		}
	}
	end := now
	if a := acts[len(acts)-1]; now.Sub(a) >= breakMin {
		end = a
	} else {
		last = now.Sub(start)
	}
	return max(longest, end.Sub(start)), total + end.Sub(start), last, breaks
}

// healthSummary is what the health view counts for a time.
type healthSummary struct {
	longest time.Duration
	breaks  int
	// crowd is how long crowdAgents or more agents worked at once.
	crowd time.Duration
	// switchPeak is the most changes of session in one hour.
	switchPeak int
	// active is how long the user acted, pauses of breakMin or more left
	// out, and dayPeak the most of it in one day.
	active, dayPeak time.Duration
}

// healthSum counts the history from from to now.
func healthSum(events []session.Event, from, now time.Time, days int, in live) healthSummary {
	events = eventsSince(events, from)
	sum := summarize(events, from, now, days, quietWho(in.shells))
	h := healthSummary{crowd: sum.together[crowdAgents-1]}
	// The stretch on now is known from the keys, as for the rules.
	since := maxTime(now.Add(-in.active), from)
	acts := humanActs(events, from, since)
	longest, active, last, breaks := stretches(acts, since)
	live := now.Sub(since)
	h.longest, h.active, h.breaks = max(longest, last+live), active+live, breaks
	dayOf := func(t time.Time) time.Time {
		y, mo, d := t.Date()
		return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
	}
	byDay := map[time.Time][]time.Time{dayOf(now): nil}
	for _, t := range acts {
		byDay[dayOf(t)] = append(byDay[dayOf(t)], t)
	}
	for day, dayActs := range byDay {
		_, active, _, _ := stretches(dayActs, minTime(day.AddDate(0, 0, 1), since))
		// shortcut: the stretch on now counts for today alone, even when
		// it started yesterday.
		if day.Equal(dayOf(now)) {
			active += live
		}
		h.dayPeak = max(h.dayPeak, active)
	}
	perHour := map[time.Time]int{}
	for _, e := range events {
		if e.K == session.KindSwitch && !e.T.Before(from) && !e.T.After(now) {
			perHour[e.T.Truncate(time.Hour)]++
		}
	}
	for _, n := range perHour {
		h.switchPeak = max(h.switchPeak, n)
	}
	return h
}

// workingNow is how many agents work now.
func (m *Model) workingNow() int {
	n := 0
	for _, s := range m.sessions {
		if !isShell(s.Adapter) && s.Status == agent.Working {
			n++
		}
	}
	return n
}

// liveHealth is what the rules know of the workspace now.
func (m *Model) liveHealth(now time.Time) live {
	in := live{working: m.workingNow()}
	if !m.lastKey.IsZero() && now.Sub(m.lastKey) < breakMin {
		in.active = now.Sub(m.activeFrom)
	}
	for _, s := range m.sessions {
		if isShell(s.Adapter) {
			in.shells = append(in.shells, s.ID)
			continue
		}
		in.unread += s.UnreadCount()
	}
	return in
}

// healthNow returns the rules that fire now.
func (m *Model) healthNow(now time.Time) []rule {
	return healthRules(m.history.Events(), m.liveHealth(now), now)
}

// health is what the health view shows.
type health struct {
	// back is what it was opened from.
	back mode
	// rng is the time shown, an index of statsRanges, and top the first
	// row on screen.
	rng, top int
	// rules and sum are what it shows, counted by countHealth rather than
	// at every frame.
	rules []rule
	sum   healthSummary
}

// countHealth counts what the health view shows, and the meter with it.
func (m *Model) countHealth(now time.Time) {
	in := m.liveHealth(now)
	m.health.rules = healthRules(m.history.Events(), in, now)
	m.health.sum = healthSum(m.statsEvents(), statsFrom(m.health.rng, now), now, statsRanges[m.health.rng], in)
	m.meter = levelOf(m.health.rules)
}

// openHealth shows the health view, or goes back from it.
func (m *Model) openHealth() tea.Cmd {
	if m.mode == modeHealth {
		m.closeHealth()
		return nil
	}
	m.health.back = modeWorkspace
	if m.mode == modeBoard || m.commandFrom == modeBoard {
		m.health.back = modeBoard
	}
	m.health.rng, m.health.top = 0, 0
	m.countHealth(time.Now())
	m.mode = modeHealth
	// The history further back than memory is the one Stats reads.
	return m.loadStats()
}

// closeHealth goes back to what the health view was opened from.
func (m *Model) closeHealth() { m.mode = m.health.back }

// healthKey handles the keys of the health view.
func (m *Model) healthKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if m.prefix {
		m.prefix = false
		// The key that opened the view closes it.
		if key == "y" {
			m.closeHealth()
			return nil
		}
		return m.runPrefix(k)
	}
	if key == m.cfg.Prefix {
		m.prefix = true
		return nil
	}
	page := max(m.height-2, 1)
	switch key {
	case "esc", "q":
		m.closeHealth()
	case "h", "left":
		m.health.rng = max(m.health.rng-1, 0)
		m.countHealth(time.Now())
	case "l", "right":
		m.health.rng = min(m.health.rng+1, len(statsRanges)-1)
		m.countHealth(time.Now())
	case "j", "down":
		m.health.top++
	case "k", "up":
		m.health.top = max(m.health.top-1, 0)
	case "space", " ", "f", "pgdown":
		m.health.top += page
	case "b", "pgup":
		m.health.top = max(m.health.top-page, 0)
	case "g", "home":
		m.health.top = 0
	case "G", "end":
		m.health.top = math.MaxInt / 2
	case "z":
		m.closeHealth()
		return m.startHold()
	}
	return nil
}

// healthHint is what the status bar says the keys of the health view do.
func (m *Model) healthHint() string {
	return "h/l time · j/k scroll · z hold · q back"
}

// renderHealth draws the health view over the whole screen but the status
// bar: the rules now, then the history of the time chosen, then what waits
// for review.
func (m *Model) renderHealth() []string {
	height := max(m.height-1, 2)
	w := max(m.width, 0)
	rules, sum := m.health.rules, m.health.sum
	wide := w >= timelineNarrow

	risk := "risk now: " + levelOf(rules).String()
	if r, ok := top(rules); ok {
		risk += " (" + r.label + ")"
	}
	title := fmt.Sprintf(" health  %s · %s", filepath.Base(m.dir), risk)
	rows := []string{ui.Reverse.Render(ui.Fit(title, w)), "", section("now · live: the last hour, the last 24 hours", w)}
	for _, r := range rules {
		rows = append(rows, ui.Fit(healthRow(r, wide), w))
		if r.lvl > levelLow || r.near() {
			hint := fmt.Sprintf("            raised %s · high %s · %s", r.of(r.raised), r.of(r.high), r.hint)
			if !wide {
				hint = "   " + r.hint
			}
			rows = append(rows, ui.Dim.Render(ui.Fit(hint, w)))
		}
	}

	var ranges []string
	for i := range statsRanges {
		name := "today"
		if statsRanges[i] > 1 {
			name = fmt.Sprintf("%d days", statsRanges[i])
		}
		if i == m.health.rng {
			name = "[" + name + "]"
		}
		ranges = append(ranges, name)
	}
	head := strings.Join(ranges, "  ") + " · from the history, h/l"
	if m.stats.loading {
		head += " · reading…"
	}
	over := func(d, mark time.Duration) string {
		if d >= mark && mark > 0 {
			return "over the raised mark of " + span(mark)
		}
		return ""
	}
	line := func(label, value, note string) string {
		if !wide {
			return ui.Fit(fmt.Sprintf("  %-6s %s", value, label), w)
		}
		return ui.Fit(fmt.Sprintf("  %-42s %-6s %s", label, value, ui.Warn.Render(note)), w)
	}
	rows = append(rows, "", section(head, w),
		line("longest stretch without a 15-minute pause", orDash(sum.longest), over(sum.longest, stretchRaised)),
		line("breaks of 15 minutes or more", fmt.Sprint(sum.breaks), ""),
	)
	if statsRanges[m.health.rng] == 1 {
		rows = append(rows, line("active time, pauses left out", orDash(sum.active), over(sum.active, activeRaised)))
	} else {
		rows = append(rows,
			line("active time, pauses left out", orDash(sum.active), ""),
			line("most active time in one day", orDash(sum.dayPeak), over(sum.dayPeak, activeRaised)))
	}
	switchNote := ""
	if sum.switchPeak >= switchesRaised {
		switchNote = fmt.Sprintf("over the raised mark of %d", switchesRaised)
	}
	rows = append(rows,
		line(fmt.Sprintf("time with %d+ agents at once, in all", crowdAgents), orDash(sum.crowd), ""),
		line("most changes of session in one hour", fmt.Sprint(sum.switchPeak), switchNote),
	)

	rows = append(rows, "", section("review · files changed and not opened", w))
	reviewed := true
	for _, s := range m.sessions {
		if n := s.UnreadCount(); n > 0 && !isShell(s.Adapter) {
			rows = append(rows, ui.Fit(fmt.Sprintf("  %-20s %d", s.Name, n), w))
			reviewed = false
		}
	}
	if reviewed {
		rows = append(rows, ui.Dim.Render(ui.Fit("  every file the agents changed in this run was opened", w)))
	} else {
		rows = append(rows, ui.Dim.Render(ui.Fit("  "+m.cfg.Prefix+" g opens the changes panel of the session on screen", w)))
	}
	rows = append(rows, "", ui.Dim.Render(ui.Fit("  Counted from keys and the history: reading with no key pressed looks like a pause.", w)))

	room := height - 1
	m.health.top = max(min(m.health.top, len(rows)-1-room), 0)
	return ui.Block(append(rows[:1], rows[1+m.health.top:]...), w, height)
}

// healthMark is the mark of a rule's level: a symbol and a word, so that
// colour is not all that tells them apart.
func healthMark(r rule) string {
	switch {
	case r.lvl == levelHigh:
		return ui.Bad.Render("■ high  ")
	case r.lvl == levelRaised:
		return ui.Warn.Render("▲ raised")
	case r.near():
		return ui.Dim.Render("◇ near  ")
	}
	return ui.Good.Render("· low   ")
}

// healthRow is the line of a rule: its level, what it counts, its value,
// how far it is to the next level and where that is. A narrow screen has no
// bar.
func healthRow(r rule, wide bool) string {
	lvl, at, more := r.next()
	next := "the highest level"
	if more {
		next = fmt.Sprintf("next: %s at %s", lvl, r.of(at))
	}
	if !wide {
		mark := []rune(ansi.Strip(healthMark(r)))[0]
		return fmt.Sprintf(" %c %s %s · %s", mark, r.short, r.of(r.val), strings.TrimPrefix(next, "next: "))
	}
	filled := 10
	if more && at > 0 {
		filled = min(r.val*10/at, 10)
	}
	bar := strings.Repeat("█", filled) + ui.Dim.Render(strings.Repeat("·", 10-filled))
	return fmt.Sprintf("  %s  %-34s %6s  %s  %s", healthMark(r), r.label, r.of(r.val), bar, next)
}

// meterBar is a bar of w cells: empty when low, half full when raised,
// full when high, in the colour of the level.
func meterBar(l level, w int) string {
	n := w * int(l) / 2
	return l.render(strings.Repeat("█", n)) + ui.Dim.Render(strings.Repeat("·", w-n))
}

// renderMeter draws the brain fry meter: the level and, when there is
// room, the rule that set it.
func (m *Model) renderMeter(r rect) []string {
	text := m.meter.render(m.meter.String())
	if t, ok := top(m.health.rules); ok && r.w-4-ansi.StringWidth(m.meter.String()+" · "+t.short) >= meterBarMin {
		text += ui.Dim.Render(" · " + t.short)
	}
	barW := max(r.w-4-ansi.StringWidth(text), 1)
	return ui.Box("brain fry", []string{meterBar(m.meter, barW) + " " + text}, r.w, r.h, false)
}
