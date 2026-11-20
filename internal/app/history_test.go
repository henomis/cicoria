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

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// historyModel returns a workspace with one Claude Code session, whose
// history is kept in memory only.
func historyModel(t *testing.T) (*Model, *session.Session) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := session.OpenLedger("", time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{
		cfg:       config.Default(),
		dir:       t.TempDir(),
		history:   l,
		delivered: map[string]time.Time{},
		turnFrom:  map[string]time.Time{},
		answered:  map[string]time.Time{},
	}
	s := session.New("s1", "api", m.dir, agent.NewClaude(nil))
	m.sessions = []*session.Session{s}
	m.track(s)
	return m, s
}

func eventsOf(m *Model, k session.Kind) []session.Event {
	var out []session.Event
	for _, e := range m.history.Events() {
		if e.K == k {
			out = append(out, e)
		}
	}
	return out
}

func TestRecordTurn(t *testing.T) {
	m, s := historyModel(t)
	now := time.Now()

	// A turn nobody sent from cicoria is one the user typed.
	m.recordTurn(s, agent.Update{TurnStarted: true, Text: "fix  the\nbug"}, now)
	s.Touched["a.go"] = now.Add(time.Second)
	s.Touched["old.go"] = now.Add(-time.Minute)
	m.recordTurn(s, agent.Update{TurnEnded: true, Answer: "done"}, now.Add(2*time.Second))

	if got := eventsOf(m, session.KindPromptTyped); len(got) != 1 || got[0].X != "fix the bug" || got[0].N != 12 {
		t.Errorf("typed prompts = %+v", got)
	}
	starts := eventsOf(m, session.KindTurnStart)
	if len(starts) != 1 || starts[0].V != "typed" {
		t.Errorf("turn starts = %+v", starts)
	}
	ends := eventsOf(m, session.KindTurnEnd)
	if len(ends) != 1 || !slices.Equal(ends[0].Files, []string{"a.go"}) || ends[0].X != "done" {
		t.Errorf("turn ends = %+v", ends)
	}

	// One cicoria just delivered is not typed.
	m.delivered[s.ID] = now.Add(3 * time.Second)
	m.recordTurn(s, agent.Update{TurnStarted: true}, now.Add(4*time.Second))
	if got := eventsOf(m, session.KindPromptTyped); len(got) != 1 {
		t.Errorf("%d typed prompts after a delivery, want 1", len(got))
	}
	if starts := eventsOf(m, session.KindTurnStart); starts[1].V != "cicoria" {
		t.Errorf("second turn from %q", starts[1].V)
	}
}

func TestStatusIsRecorded(t *testing.T) {
	m, s := historyModel(t)
	s.Apply(agent.Update{HasStatus: true, Status: agent.Working}, true, time.Now())
	s.Apply(agent.Update{HasStatus: true, Status: agent.NeedsInput}, true, time.Now())
	got := eventsOf(m, session.KindStatus)
	if len(got) != 2 || got[0].V != "working" || got[1].V != "needs input" || got[0].S != s.ID {
		t.Errorf("statuses = %+v", got)
	}
}

func TestQueueIsRecorded(t *testing.T) {
	m, s := historyModel(t)
	m.cfg.Timeline.Text = session.TextNone
	m.enqueueFrom(s, session.Message{Text: "secret", From: fromNotes}, session.FromWeb)
	got := eventsOf(m, session.KindQueue)
	if len(got) != 1 || got[0].V != fromNotes || got[0].Src != session.FromWeb || got[0].N != 6 || got[0].X != "" {
		t.Errorf("queued = %+v", got)
	}
}

func TestAway(t *testing.T) {
	m, _ := historyModel(t)
	t0 := time.Now()
	m.noteKey(t0)
	m.noteKey(t0.Add(time.Minute))
	if !m.awayFrom.IsZero() {
		t.Errorf("away from %v after a minute", m.awayFrom)
	}
	m.noteKey(t0.Add(time.Minute + awayAfter))
	if !m.awayFrom.Equal(t0.Add(time.Minute)) {
		t.Errorf("away from %v, want the key before the gap", m.awayFrom)
	}
}

// timelineModel returns a workspace with two sessions and an hour of
// history: one that worked, waited and was answered, and one that was
// closed.
func timelineModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m, s := historyModel(t)
	m.width, m.height = width, height
	now := time.Now()
	at := func(min int) time.Time { return now.Add(time.Duration(min-60) * time.Minute) }
	for _, e := range []session.Event{
		{T: at(0), K: session.KindRunStart},
		{T: at(1), S: s.ID, K: session.KindStatus, V: "working"},
		{T: at(2), S: s.ID, K: session.KindQueue, X: "add pagination"},
		{T: at(10), S: s.ID, K: session.KindStatus, V: "needs input"},
		{T: at(25), S: s.ID, K: session.KindAnswer, V: "Yes", Src: session.FromTUI},
		{T: at(25), S: s.ID, K: session.KindStatus, V: "working"},
		{T: at(26), S: s.ID, K: session.KindPromptTyped, X: "fix it"},
		{T: at(26), S: s.ID, K: session.KindTurnStart, V: "typed"},
		{T: at(40), S: s.ID, K: session.KindTurnEnd, Files: []string{"api.go"}, X: "done"},
		{T: at(40), S: s.ID, K: session.KindStatus, V: "idle"},
		{T: at(5), S: "gone", K: session.KindSessionOpen, V: "docs"},
		{T: at(6), S: "gone", K: session.KindStatus, V: "working"},
		{T: at(30), S: "gone", K: session.KindSessionClose, V: "docs"},
	} {
		m.history.Record(e)
	}
	s.Status = agent.Idle
	return m
}

func TestTimelineRenders(t *testing.T) {
	for _, width := range []int{1, 40, 59, 60, 80, 160} {
		for _, height := range []int{1, 3, 10, 30} {
			m := timelineModel(t, width, height)
			m.openTimeline()
			rows := m.renderTimeline()
			if len(rows) > max(height-1, 2) {
				t.Errorf("%dx%d: %d rows", width, height, len(rows))
			}
		}
	}
	m := timelineModel(t, 100, 24)
	m.openTimeline()
	out := ansi.Strip(strings.Join(m.renderTimeline(), "\n"))
	t.Log("\n" + out)
	for _, want := range []string{"api", "docs", "█", "✓", "agents waited for you 15m", "now", "answered “Yes” · it waited 15m", "queued “add pagination”", "closed it", "typed “fix it” → done in 14m, 1 file"} {
		if !strings.Contains(out, want) {
			t.Errorf("the timeline does not show %q", want)
		}
	}
}

func TestTimelineKeys(t *testing.T) {
	m := timelineModel(t, 100, 24)
	m.openTimeline()
	if m.mode != modeTimeline {
		t.Fatal("the timeline did not open")
	}
	key := func(k string) {
		r := []rune(k)
		m.timelineKey(tea.KeyPressMsg{Code: r[0], Text: k})
	}
	_, _, entries := m.timelineData(time.Now())
	// The newest first: docs closed, then api typed and answered.
	if len(entries) != 4 || m.cursor(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	key("j")
	key("j")
	if m.cursor(entries) != 2 || !strings.Contains(entries[2].text, "Yes") {
		t.Fatalf("j moved to %d", m.cursor(entries))
	}
	// The answer led to the turn that followed it.
	key("d")
	if tv := m.timeline.turn; tv == nil || len(tv.files) != 1 || tv.said != "done" {
		t.Fatalf("turn = %+v", m.timeline.turn)
	}
	key("q")
	if m.timeline.turn != nil {
		t.Error("q did not close the turn")
	}
	key("-")
	// An hour shown, so a longer time is two.
	if m.timeline.rng != 2 {
		t.Errorf("- shows range %d, want two hours", m.timeline.rng)
	}
	key("+")
	key("+")
	key("+")
	if m.timeline.rng != 0 {
		t.Errorf("+ shows range %d, want the shortest", m.timeline.rng)
	}
	key("s")
	if m.timeline.rng != sinceAway {
		t.Error("s did not go back to the time since you left")
	}
	key("q")
	if m.mode != modeWorkspace {
		t.Error("q did not close the timeline")
	}
}

// As time passes the bars keep the length of their cells and now stays in
// the last one: they move left by whole cells.
func TestTimelineBarsSlide(t *testing.T) {
	m := timelineModel(t, 100, 24)
	m.openTimeline()
	now := time.Now()
	t0, _, _ := m.timelineData(now)
	for _, d := range []time.Duration{time.Second, time.Minute, time.Hour} {
		t1, _, _ := m.timelineData(now.Add(d))
		if t1.Step != t0.Step {
			t.Errorf("%v later a cell lasts %v, not %v", d, t1.Step, t0.Step)
		}
		if c := t1.CellAt(t1.Now); c != t1.Cells()-1 {
			t.Errorf("%v later now is in cell %d of %d", d, c, t1.Cells())
		}
		if moved := t1.From.Sub(t0.From); moved%t0.Step != 0 {
			t.Errorf("%v later the bars moved by %v, not whole cells of %v", d, moved, t0.Step)
		}
	}
	// Another time is laid out at once.
	m.timeline.rng = 3
	t3, _, _ := m.timelineData(now)
	if t3.Step == t0.Step {
		t.Error("four hours are drawn with the cells of one")
	}
}

// linesOf returns the lines under the bars of a workspace whose history is
// the given events, from an hour ago.
func linesOf(t *testing.T, m *Model, events func(at func(int) time.Time) []session.Event) []entry {
	t.Helper()
	now := time.Now()
	at := func(min int) time.Time { return now.Add(time.Duration(min-60) * time.Minute) }
	for _, e := range events(at) {
		m.history.Record(e)
	}
	m.awayFrom, m.timeline.rng = at(0), sinceAway
	m.width, m.height = 120, 30
	_, _, lines := m.timelineData(now)
	return lines
}

func texts(lines []entry) string {
	var b []string
	for _, e := range lines {
		b = append(b, e.text+e.outcome)
	}
	return strings.Join(b, "\n")
}

func TestLinesSayWhatCameOfThem(t *testing.T) {
	m, s := historyModel(t)
	s.Status, s.Since = agent.NeedsInput, time.Now().Add(-2*time.Minute)
	s.Queue = []session.Message{{Text: "later"}}
	lines := linesOf(t, m, func(at func(int) time.Time) []session.Event {
		id := s.ID
		return []session.Event{
			// Queued, sent five minutes later, done.
			{T: at(1), S: id, K: session.KindQueue, X: "add pagination", N: 14},
			{T: at(6), S: id, K: session.KindSend, X: "add pagination", N: 14},
			{T: at(6), S: id, K: session.KindTurnStart},
			{T: at(20), S: id, K: session.KindTurnEnd, Files: []string{"a", "b"}, X: "it is in"},
			// Sent at once, failed: one line says both.
			{T: at(30), S: id, K: session.KindSend, V: fromNotes, Src: session.FromTUI},
			{T: at(30), S: id, K: session.KindTurnStart},
			{T: at(34), S: id, K: session.KindTurnFailed},
			// A card started in a worktree is one line.
			{T: at(40), S: id, K: session.KindCardStart, V: "login form"},
			{T: at(40), S: id, K: session.KindWorktreeAdd, V: "/w/login"},
			{T: at(40), S: id, K: session.KindQueue, V: fromCard, X: "login form", N: 10},
			{T: at(40), S: id, K: session.KindSend, V: fromCard, X: "login form", N: 10},
			{T: at(40), S: id, K: session.KindTurnStart},
			{T: at(45), S: id, K: session.KindTurnEnd},
			// Still in the queue.
			{T: at(50), S: id, K: session.KindQueue, X: "later", N: 5},
			// Typed, and now it waits.
			{T: at(55), S: id, K: session.KindPromptTyped, X: "fix it"},
			{T: at(55), S: id, K: session.KindTurnStart},
		}
	})
	got := texts(lines)
	for _, want := range []string{
		"queued “add pagination” → sent ",
		", done in 14m, 2 files",
		"sent review notes → ✕ failed after 4m",
		"started the card “login form” in /w/login → done in 5m",
		"queued “later” → still in the queue",
		"typed “fix it” → worked ",
		"waits for you since ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the lines do not say %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"a turn failed", "made the worktree", "working,"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the lines say %q:\n%s", unwanted, got)
		}
	}
	if said := lines[len(lines)-1].said; said != "it is in" {
		t.Errorf("the first line says the agent said %q", said)
	}
}

func TestNowLines(t *testing.T) {
	m, s := historyModel(t)
	web := session.New("s2", "web", m.dir, agent.NewClaude(nil))
	m.sessions = append(m.sessions, web)
	s.Status, s.Since = agent.NeedsInput, time.Now().Add(-5*time.Minute)
	s.Question, s.Tool = "Claude needs your permission to use Bash", agent.Tool{Name: "Bash", Detail: "npm test"}
	web.Status, web.Since = agent.Working, time.Now().Add(-9*time.Minute)
	lines := linesOf(t, m, func(at func(int) time.Time) []session.Event {
		return []session.Event{
			{T: at(50), S: web.ID, K: session.KindPromptTyped, X: "use the Button"},
			{T: at(50), S: web.ID, K: session.KindTurnStart},
		}
	})
	if len(lines) < 2 || !lines[0].now || !lines[1].now {
		t.Fatalf("lines = %s", texts(lines))
	}
	if want := "waits for you 5m: “Claude needs your permission to use Bash: npm test”"; lines[0].kind != '▲' || lines[0].text != want {
		t.Errorf("waiting line = %q, want %q", lines[0].text, want)
	}
	if !strings.Contains(lines[1].text, "working 9m on “use the Button”") {
		t.Errorf("working line = %q", lines[1].text)
	}
	// It opens on what waits for you.
	m.openTimelineOn(nil)
	if i := m.cursor(lines); i != 0 {
		t.Errorf("the cursor opens on line %d", i)
	}
	m.timeline.sel = lines[len(lines)-1].key()
	m.timelineKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if i := m.cursor(lines); i != 0 {
		t.Errorf("a went to line %d, not the session that waits", i)
	}
}

func TestCursorKeepsItsLine(t *testing.T) {
	m, s := historyModel(t)
	lines := linesOf(t, m, func(at func(int) time.Time) []session.Event {
		return []session.Event{
			{T: at(10), S: s.ID, K: session.KindQueue, X: "one", N: 3},
			{T: at(10), S: s.ID, K: session.KindQueue, X: "two", N: 3},
			{T: at(40), S: s.ID, K: session.KindQueue, X: "three", N: 5},
		}
	})
	if lines[1].key() == lines[2].key() {
		t.Error("two lines of one moment have the same key")
	}
	m.timeline.sel = lines[2].key()
	if i := m.cursor(lines); i != 2 {
		t.Errorf("the cursor is on %d, want 2", i)
	}
	// A line gone from the time shown gives way to the nearest one.
	gone := lines[2].key()
	gone.at = gone.at.Add(-time.Hour)
	m.timeline.sel = gone
	if i := m.cursor(lines); !lines[i].at.Equal(lines[2].at) {
		t.Errorf("after its line went the cursor is on %d, not on one of the oldest", i)
	}
}

func TestTurnKeys(t *testing.T) {
	m := timelineModel(t, 100, 24)
	m.openTimeline()
	key := func(k string) {
		r := []rune(k)
		m.timelineKey(tea.KeyPressMsg{Code: r[0], Text: k})
	}
	key("j")
	key("j")
	key("d")
	if m.timeline.turn == nil {
		t.Fatal("d showed no turn")
	}
	// A key it does not use leaves it open.
	key("m")
	if m.timeline.turn == nil {
		t.Error("m closed the turn")
	}
	// The prefix is the prefix.
	m.timelineKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	key("e")
	if m.mode == modeTimeline {
		t.Error("ctrl+a e did not close the timeline over a turn")
	}

	// q in what a turn opened comes back to the timeline.
	m.mode, m.view.back = modeView, modeTimeline
	m.viewKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.mode != modeTimeline {
		t.Errorf("q went to mode %d, not back to the timeline", m.mode)
	}
}

// A card's line says what came of it from what is recorded when a card
// starts: its prompt goes out through the queue without a line of its own.
func TestCardLineAsRecorded(t *testing.T) {
	m, s := historyModel(t)
	lines := linesOf(t, m, func(at func(int) time.Time) []session.Event {
		return []session.Event{
			{T: at(10), S: s.ID, K: session.KindCardStart, V: "login form", N: 10, X: "login form"},
			{T: at(11), S: s.ID, K: session.KindSend, V: fromCard, N: 10, X: "login form"},
			{T: at(11), S: s.ID, K: session.KindTurnStart},
			{T: at(20), S: s.ID, K: session.KindTurnEnd},
		}
	})
	if got := texts(lines); !strings.Contains(got, "started the card “login form” → done in 9m") {
		t.Errorf("lines:\n%s", got)
	}
}

// ctrl+a e over the board, or the palette there, goes back to the board.
func TestTimelineFromBoardGoesBack(t *testing.T) {
	m := timelineModel(t, 100, 24)
	m.mode = modeBoard
	m.runCommand(false, (*Model).openTimeline)
	if m.mode != modeTimeline {
		t.Fatal("the timeline did not open")
	}
	m.timelineKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.mode != modeBoard {
		t.Errorf("q went to mode %d, not back to the board", m.mode)
	}
}

// A turn over a screen too short for it does not break the drawing.
func TestTurnOnTinyScreen(t *testing.T) {
	m := timelineModel(t, 100, 24)
	m.openTimeline()
	m.timeline.turn = &turnView{name: "api", start: time.Now(), end: time.Now()}
	for _, width := range []int{1, 10, 40} {
		for height := 1; height <= 6; height++ {
			m.width, m.height = width, height
			_ = m.renderTimeline()
		}
	}
}

// A run that ended without saying so, as when the terminal was closed,
// does not stretch the last status of its sessions over the time cicoria
// was not running.
func TestRunThatDidNotEnd(t *testing.T) {
	m, s := historyModel(t)
	now := time.Now()
	at := func(min int) time.Time { return now.Add(time.Duration(min-60) * time.Minute) }
	for _, e := range []session.Event{
		{T: at(0), K: session.KindRunStart},
		{T: at(1), S: s.ID, K: session.KindStatus, V: "working"},
		{T: at(5), S: s.ID, K: session.KindQueue, X: "last thing"},
		// No run_end: cicoria was killed. It came back 50 minutes later.
		{T: at(55), K: session.KindRunStart},
		{T: at(55), S: s.ID, K: session.KindStatus, V: "idle"},
	} {
		m.history.Record(e)
	}
	m.awayFrom, m.timeline.rng, m.width, m.height = at(0), sinceAway, 120, 30
	_, lanes, _ := m.timelineData(now)
	for _, p := range lanes[0].periods {
		if p.State == ui.StateWorking && (p.To.IsZero() || p.To.After(at(5))) {
			t.Errorf("working from %v to %v, past the last sign of life at %v", p.From, p.To, at(5))
		}
	}
}

// A cicoria that opens on a workspace another one has open records
// nothing until the user says to open it there too.
func TestSecondCicoriaRecordsNothingYet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := &Model{cfg: config.Default(), dir: t.TempDir(), twice: true}
	m.openHistory(false)
	if got := m.history.Events(); len(got) != 0 {
		t.Errorf("recorded before it was agreed: %+v", got)
	}
	m.beginRun()
	if got := m.history.Events(); len(got) != 1 || got[0].K != session.KindRunStart {
		t.Errorf("once agreed: %+v", got)
	}
}

// TestClosedLanesKeepTheirOrder checks that the bars of closed sessions are
// in the same order at every redraw, the last closed first, and not in the
// order of a map, which changes each time and makes them flicker.
func TestClosedLanesKeepTheirOrder(t *testing.T) {
	at := time.Date(2026, 11, 20, 10, 6, 0, 0, time.UTC)
	byID := map[string][]session.Event{
		"live":   {{S: "live", T: at}},
		"old":    {{S: "old", T: at.Add(-2 * time.Hour)}},
		"shell":  {{S: "shell", T: at}},
		"pack":   {{S: "pack", T: at}},
		"stiggy": {{S: "stiggy", T: at.Add(time.Second)}},
	}
	want := []string{"stiggy", "pack", "shell"}
	for range 50 {
		if got := closedIDs(byID, []string{"live"}, at.Add(-time.Hour)); !slices.Equal(got, want) {
			t.Fatalf("closedIDs = %v, want %v", got, want)
		}
	}
}
