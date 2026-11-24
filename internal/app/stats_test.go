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
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/session"
)

func TestSummarize(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 24, h, m, 0, 0, time.Local) }
	events := []session.Event{
		// Yesterday: out of today's range.
		{T: at(-10, 0), S: "a", K: session.KindTurnStart, V: "typed"},
		{T: at(9, 0), K: session.KindRunStart},
		{T: at(10, 0), S: "a", K: session.KindStatus, V: "working"},
		{T: at(10, 0), S: "a", K: session.KindTurnStart, V: "typed"},
		{T: at(10, 2), S: "a", K: session.KindTurnEnd, Files: []string{"x.go", "y.go"}},
		{T: at(10, 2), S: "a", K: session.KindStatus, V: "needs input"},
		{T: at(10, 7), S: "a", K: session.KindStatus, V: "working"},
		{T: at(10, 7), S: "a", K: session.KindTurnStart, V: "cicoria"},
		{T: at(10, 17), S: "a", K: session.KindInterrupted},
		{T: at(10, 17), S: "a", K: session.KindStatus, V: "idle"},
		// b works alongside a for 7 minutes.
		{T: at(10, 10), S: "b", K: session.KindStatus, V: "working"},
		// cicoria closed while b waited: the wait ends then.
		{T: at(11, 0), S: "b", K: session.KindTurnStart, V: "typed"},
		{T: at(11, 1), S: "b", K: session.KindTurnFailed, Files: []string{"x.go"}},
		{T: at(11, 1), S: "b", K: session.KindStatus, V: "needs input"},
		{T: at(11, 4), K: session.KindRunEnd},
		// b waits again, until now.
		{T: at(14, 0), K: session.KindRunStart},
		{T: at(14, 50), S: "b", K: session.KindStatus, V: "needs input"},
		{T: at(14, 0), S: "shell", K: session.KindTurnStart, V: "typed"},
	}
	name := func(id string) string { return id }
	closed := func(string) bool { return false }
	w := who{name: name, agent: func(id string) string { return map[string]string{"a": "claude"}[id] }, closed: closed, shells: []string{"shell"}}
	sum := summarize(events, statsFrom(0, now), now, 1, w)

	if sum.turns != 3 || sum.typed != 2 || sum.sent != 1 || sum.interrupted != 1 || sum.failed != 1 {
		t.Errorf("counts = %d turns, %d typed, %d sent, %d interrupted, %d failed", sum.turns, sum.typed, sum.sent, sum.interrupted, sum.failed)
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 10 * time.Minute}
	if len(sum.lengths) != 3 || sum.lengths[0] != want[0] || sum.lengths[1] != want[1] || sum.lengths[2] != want[2] {
		t.Errorf("lengths = %v", sum.lengths)
	}
	if quantile(sum.lengths, 0.5) != 2*time.Minute {
		t.Errorf("median = %v", quantile(sum.lengths, 0.5))
	}
	var waited time.Duration
	for _, w := range sum.waits {
		waited += w
	}
	// a 5m, b 3m before cicoria closed and 10m until now.
	if len(sum.waits) != 3 || waited != 18*time.Minute {
		t.Errorf("waits = %v", sum.waits)
	}
	if sum.hours[10] != 2 || sum.hours[11] != 1 {
		t.Errorf("hours = %v", sum.hours)
	}
	if len(sum.sessions) != 2 || sum.sessions[0].id != "a" || sum.sessions[0].files != 2 || sum.sessions[0].busy != 12*time.Minute {
		t.Errorf("sessions = %+v", sum.sessions)
	}
	// a ended a turn at 10:02 and got the next at 10:07.
	if len(sum.idle) != 1 || sum.idle[0] != 5*time.Minute {
		t.Errorf("idle = %v", sum.idle)
	}
	// a worked 12m and b 51m, 7m of it at once.
	if sum.together[0] != 49*time.Minute || sum.together[1] != 7*time.Minute {
		t.Errorf("together = %v", sum.together)
	}
	if len(sum.agents) != 2 || sum.agents[0].name != "claude" || sum.agents[0].turns != 2 || sum.agents[0].interrupted != 1 || sum.agents[0].asks != 1 ||
		sum.agents[1].name != notRecorded || sum.agents[1].asks != 2 {
		t.Errorf("agents = %+v", sum.agents)
	}
	if len(sum.files) != 2 || sum.files[0].path != "x.go" || sum.files[0].n != 2 {
		t.Errorf("files = %+v", sum.files)
	}

	// Seven days count yesterday's turn too, in its own day.
	sum = summarize(events, statsFrom(1, now), now, 7, w)
	if sum.turns != 4 || sum.buckets != 7 || sum.typedBy[5] != 1 || sum.typedBy[6] != 2 || sum.sentBy[6] != 1 {
		t.Errorf("seven days: %d turns, typed %v, sent %v", sum.turns, sum.typedBy, sum.sentBy)
	}
}

func TestStatsRenders(t *testing.T) {
	for _, width := range []int{1, 40, 59, 60, 80, 99, 100, 160} {
		for _, height := range []int{1, 3, 10, 40} {
			for _, numbers := range []bool{false, true} {
				m := timelineModel(t, width, height)
				m.openStats()
				m.stats.numbers = numbers
				rows := m.renderStats()
				if len(rows) > max(height-1, 2) {
					t.Errorf("%dx%d: %d rows", width, height, len(rows))
				}
				for _, r := range rows {
					if w := ansi.StringWidth(r); w > max(width, 0) {
						t.Errorf("%dx%d, numbers %v: a row is %d wide", width, height, numbers, w)
					}
				}
			}
		}
	}
	m := timelineModel(t, 120, 60)
	m.openStats()
	out := ansi.Strip(strings.Join(m.renderStats(), "\n"))
	t.Log("\n" + out)
	for _, want := range []string{"stats", "the last 7 days", "when", "turns per day", "turn length", "agents at once", "typed by you", "api", "api.go", "waited on you"} {
		if !strings.Contains(out, want) {
			t.Errorf("the charts do not show %q", want)
		}
	}
	m.stats.numbers = true
	out = ansi.Strip(strings.Join(m.renderStats(), "\n"))
	for _, want := range []string{"turns by hour", "turns took", "typed by you"} {
		if !strings.Contains(out, want) {
			t.Errorf("the numbers do not show %q", want)
		}
	}
}

func TestStatsKeys(t *testing.T) {
	m := timelineModel(t, 100, 30)
	key := func(k string) tea.Cmd {
		r := []rune(k)
		return m.statsKey(tea.KeyPressMsg{Code: r[0], Text: k})
	}
	m.openStats()
	if m.mode != modeStats || m.stats.rng != 1 {
		t.Fatalf("Stats did not open on seven days: mode %d, range %d", m.mode, m.stats.rng)
	}
	key("h")
	key("h")
	if m.stats.rng != 0 {
		t.Errorf("h shows range %d", m.stats.rng)
	}
	key("l")
	key("l")
	key("l")
	if m.stats.rng != len(statsRanges)-1 {
		t.Errorf("l shows range %d", m.stats.rng)
	}
	key("t")
	if !m.stats.numbers || !strings.Contains(m.statsHint(), "t charts") {
		t.Error("t did not show the numbers")
	}
	key("t")
	if m.stats.numbers {
		t.Error("t did not go back to the charts")
	}
	key("q")
	if m.mode != modeWorkspace {
		t.Error("q did not close Stats")
	}

	// Opened from the board, it goes back there.
	m.mode = modeBoard
	m.openStats()
	key("q")
	if m.mode != modeBoard {
		t.Errorf("q went to mode %d, not the board", m.mode)
	}

	// j and k scroll, and stop at the ends.
	m.mode = modeWorkspace
	m.height = 10
	m.openStats()
	key("k")
	if m.stats.top != 0 {
		t.Errorf("k scrolled to %d from the top", m.stats.top)
	}
	key("j")
	key("j")
	m.renderStats()
	if m.stats.top != 2 {
		t.Errorf("j j scrolled to %d", m.stats.top)
	}
	key("G")
	m.renderStats()
	if last := m.stats.top; last < 2 || last > 200 {
		t.Errorf("G scrolled to %d", last)
	}
}

func TestStatsEventsJoinFileAndMemory(t *testing.T) {
	m, s := historyModel(t)
	now := time.Now()
	m.history.Record(session.Event{T: now.Add(-time.Hour), S: s.ID, K: session.KindTurnStart, V: "typed"})
	m.stats.file = []session.Event{
		{T: now.Add(-20 * 24 * time.Hour), S: s.ID, K: session.KindTurnStart, V: "typed"},
		// Also in memory: counted once.
		{T: now.Add(-time.Hour), S: s.ID, K: session.KindTurnStart, V: "typed"},
	}
	if got := m.statsEvents(); len(got) != 2 {
		t.Errorf("events = %+v", got)
	}
}

func TestSessionOpenRecordsAgent(t *testing.T) {
	m, s := historyModel(t)
	m.record(s, session.Event{K: session.KindSessionOpen, V: s.Name})
	if got := eventsOf(m, session.KindSessionOpen); len(got) != 1 || got[0].Agent != s.Adapter.Name() || got[0].Agent == "" {
		t.Errorf("session open = %+v", got)
	}
}

func TestTogether(t *testing.T) {
	t0 := time.Now()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	// Four spans overlap for a minute, and one ends as another starts.
	got := together([][2]time.Time{{at(0), at(10)}, {at(5), at(10)}, {at(6), at(10)}, {at(9), at(10)}, {at(10), at(12)}, {at(3), at(3)}})
	want := [4]time.Duration{7 * time.Minute, time.Minute, 3 * time.Minute, time.Minute}
	if got != want {
		t.Errorf("together = %v, want %v", got, want)
	}
}

func TestHistogram(t *testing.T) {
	got := histogram([]time.Duration{time.Second, 10 * time.Second, 11 * time.Second, time.Minute, time.Hour})
	// A time on a bound goes in the bucket above it: 10s is not under 10s.
	want := []int{1, 2, 0, 1, 0, 0, 1}
	if !slices.Equal(got, want) {
		t.Errorf("histogram = %v, want %v", got, want)
	}
}

func TestAtOnce(t *testing.T) {
	t0 := time.Now()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	// Over 4 slices of 10 minutes: one span, then two, then none, then one
	// that ends as another starts, which is never two.
	spans := [][2]time.Time{{at(0), at(20)}, {at(12), at(18)}, {at(30), at(35)}, {at(35), at(40)}}
	if got := atOnce(spans, at(0), at(40), 4); !slices.Equal(got, []int{1, 2, 0, 1}) {
		t.Errorf("atOnce = %v", got)
	}
}
