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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

func TestHealthRules(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 24, h, m, 0, 0, time.Local) }
	now := at(15, 0)
	working := func(from time.Time, ids ...string) []session.Event {
		var out []session.Event
		for _, id := range ids {
			out = append(out, session.Event{T: from, S: id, K: session.KindStatus, V: "working"})
		}
		return out
	}
	switches := func(n int) []session.Event {
		var out []session.Event
		for i := range n {
			out = append(out, session.Event{T: now.Add(-time.Duration(i+1) * time.Minute), S: "a", K: session.KindSwitch})
		}
		return out
	}
	// prompts are typed every 10 minutes for some hours, ending at an hour.
	prompts := func(from time.Time, hours int) []session.Event {
		var out []session.Event
		for t := from; !t.After(from.Add(time.Duration(hours) * time.Hour)); t = t.Add(10 * time.Minute) {
			out = append(out, session.Event{T: t, S: "a", K: session.KindPromptTyped})
		}
		return out
	}
	day := live{}
	with := func(f func(*live)) live { in := day; f(&in); return in }

	for _, c := range []struct {
		name   string
		events []session.Event
		in     live
		now    time.Time
		want   level
		rules  int
	}{
		{"nothing", nil, with(func(l *live) { l.active = 30 * time.Minute }), now, levelLow, 0},
		{"long stretch", nil, with(func(l *live) { l.active = 2 * time.Hour }), now, levelRaised, 1},
		{"longer stretch", nil, with(func(l *live) { l.active = 3 * time.Hour }), now, levelHigh, 1},
		{"crowd for long", working(at(14, 20), "a", "b", "c", "d"), day, now, levelHigh, 1},
		{"crowd for a while", working(at(14, 45), "a", "b", "c", "d"), day, now, levelRaised, 1},
		{"crowd right now", nil, with(func(l *live) { l.working = 4 }), now, levelRaised, 1},
		{"switches", switches(25), day, now, levelRaised, 1},
		{"many switches", switches(45), day, now, levelHigh, 1},
		{"unread", nil, with(func(l *live) { l.unread = 12 }), now, levelRaised, 1},
		{"active for long", prompts(at(5, 0), 5), day, now, levelRaised, 1},
		{"active for longer", prompts(at(6, 0), 7), day, at(16, 0), levelHigh, 1},
		{"active yesterday", prompts(at(5, 0), 7), day, at(5, 0).Add(30 * time.Hour), levelLow, 0},
		// Three hours of prompts, then 1h20 of keys on now with no prompt:
		// 4h20 in all.
		{"active with keys now", prompts(at(9, 0), 3), with(func(l *live) { l.active = 80 * time.Minute }), at(13, 20), levelRaised, 1},
		{"near but low", prompts(at(9, 0), 3), day, at(12, 5), levelLow, 0},
		{"highest wins", working(at(14, 20), "a", "b", "c", "d"), with(func(l *live) { l.active = 2 * time.Hour }), now, levelHigh, 2},
	} {
		rules := healthRules(c.events, c.in, c.now)
		fired := 0
		for _, r := range rules {
			if r.lvl > levelLow {
				fired++
			}
		}
		if got := levelOf(rules); got != c.want || fired != c.rules || len(rules) != 5 {
			t.Errorf("%s: level %s with %d rule(s) up of %d %v, want %s with %d", c.name, got, fired, len(rules), rules, c.want, c.rules)
		}
	}
}

func TestStretches(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 24, h, m, 0, 0, time.Local) }
	acts := []time.Time{at(10, 0), at(10, 10), at(10, 40), at(10, 50)}
	if longest, total, last, breaks := stretches(acts, at(10, 55)); longest != 15*time.Minute || total != 25*time.Minute || last != 15*time.Minute || breaks != 1 {
		t.Errorf("still at it: longest %s, total %s, %d break(s)", longest, total, breaks)
	}
	if longest, total, last, breaks := stretches(acts, at(12, 0)); longest != 10*time.Minute || total != 20*time.Minute || last != 0 || breaks != 1 {
		t.Errorf("gone since: longest %s, total %s, %d break(s)", longest, total, breaks)
	}
	if longest, total, _, breaks := stretches(nil, at(12, 0)); longest != 0 || total != 0 || breaks != 0 {
		t.Errorf("nothing done: longest %s, total %s, %d break(s)", longest, total, breaks)
	}
}

func TestHealthSum(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 24, h, m, 0, 0, time.Local) }
	events := []session.Event{
		{T: at(10, 0), S: "a", K: session.KindTurnStart, V: "typed"},
		{T: at(10, 1), S: "a", K: session.KindSwitch},
		{T: at(10, 2), S: "b", K: session.KindSwitch},
		{T: at(10, 3), S: "a", K: session.KindSwitch},
		{T: at(11, 0), S: "b", K: session.KindSwitch},
		{T: at(20, 0), S: "a", K: session.KindPromptTyped},
		{T: at(20, 5), S: "a", K: session.KindAnswer, Src: session.FromWeb},
		// Sent by the queue: not something the user did then.
		{T: at(20, 30), S: "a", K: session.KindSend, Src: ""},
	}
	h := healthSum(events, statsFrom(0, at(21, 0)), at(21, 0), 1, live{})
	if h.switchPeak != 3 {
		t.Errorf("health sum = %+v", h)
	}
	// Acts at 10:01-10:03, 11:00 and 20:00-20:05: two breaks between them.
	if h.breaks != 2 || h.longest != 5*time.Minute || h.active != 7*time.Minute || h.dayPeak != 7*time.Minute {
		t.Errorf("stretches: longest %s, active %s, most in a day %s, %d break(s)", h.longest, h.active, h.dayPeak, h.breaks)
	}
	// Over two days, the busier one is the most in one day.
	events = append(events, session.Event{T: at(10, 0).AddDate(0, 0, 1), S: "a", K: session.KindPromptTyped}, session.Event{T: at(10, 10).AddDate(0, 0, 1), S: "a", K: session.KindPromptTyped})
	h = healthSum(events, statsFrom(1, at(21, 0).AddDate(0, 0, 1)), at(21, 0).AddDate(0, 0, 1), 7, live{})
	if h.active != 17*time.Minute || h.dayPeak != 10*time.Minute {
		t.Errorf("two days: active %s, most in a day %s", h.active, h.dayPeak)
	}
}

func TestHealthKeys(t *testing.T) {
	m := timelineModel(t, 100, 30)
	key := func(k string) tea.Cmd {
		r := []rune(k)
		return m.healthKey(tea.KeyPressMsg{Code: r[0], Text: k})
	}
	m.openHealth()
	if m.mode != modeHealth || m.health.rng != 0 {
		t.Fatalf("health did not open on today: mode %d, range %d", m.mode, m.health.rng)
	}
	key("l")
	if m.health.rng != 1 {
		t.Errorf("l shows range %d", m.health.rng)
	}
	for _, width := range []int{1, 40, 100} {
		for _, height := range []int{1, 3, 30} {
			m.width, m.height = width, height
			if rows := m.renderHealth(); len(rows) != max(height-1, 2) {
				t.Errorf("%dx%d: %d rows", width, height, len(rows))
			}
		}
	}
	m.width, m.height = 100, 40
	if text := ansi.Strip(strings.Join(m.renderHealth(), "\n")); !strings.Contains(text, "risk now: low") || !strings.Contains(text, "next: raised at 1h30") || !strings.Contains(text, "review") {
		t.Errorf("the health view misses its parts:\n%s", text)
	}
	// A narrow screen draws the rules without bars.
	m.width = 40
	for _, row := range m.renderHealth()[3:8] {
		if strings.Contains(row, "█") {
			t.Errorf("a bar at 40 columns: %q", ansi.Strip(row))
		}
	}
	m.width = 100
	key("q")
	if m.mode != modeWorkspace {
		t.Error("q did not close the health view")
	}
	m.openHealth()
	key("z")
	if m.mode != modeWorkspace || m.hold == nil {
		t.Errorf("z did not start a hold: mode %d", m.mode)
	}
}

func TestHold(t *testing.T) {
	m, s := historyModel(t)
	m.width, m.height = 160, 30
	other := session.New("s2", "docs", m.dir, agent.NewClaude(nil))
	mine := session.New("s3", "web", m.dir, agent.NewClaude(nil))
	mine.Paused = true
	m.sessions = append(m.sessions, other, mine)
	s.Queue = []session.Message{{Text: "later"}}
	key := func(k string) { m.handleKey(tea.KeyPressMsg{Code: []rune(k)[0], Text: k}) }

	m.startHold()
	if m.hold == nil || !s.Paused || !other.Paused || m.prompt == nil {
		t.Fatalf("the hold did not pause everything: hold %v, paused %v %v", m.hold, s.Paused, other.Paused)
	}
	m.prompt.done("check the 429 path")
	m.prompt = nil
	// Killed now, cicoria would bring the queues back as they were.
	st, err := session.LoadState(session.StatePath(m.dir))
	if err != nil || len(st.Sessions) != 3 || st.Sessions[0].Paused || st.Sessions[1].Paused || !st.Sessions[2].Paused {
		t.Errorf("saved during the hold: %+v, %v", st.Sessions, err)
	}
	// The hold takes the screen, in place of the agents, with the note.
	if text := ansi.Strip(strings.Join(m.renderHold(), "\n")); !strings.Contains(text, "you wrote: check the 429 path") || !strings.Contains(text, "of 15m") || !strings.Contains(text, "enter: back to work") {
		t.Errorf("hold screen:\n%s", text)
	}

	// A key pressed to wake the screen does not end the break, nor counts
	// as the user back; enter does.
	before := m.lastKey
	key("x")
	if m.hold == nil || !m.lastKey.Equal(before) {
		t.Fatalf("a key other than enter ended the hold, or counted: hold %v, last key moved %v", m.hold, !m.lastKey.Equal(before))
	}
	// An agent that exited during the hold is sent nothing when it ends.
	other.Queue = []session.Message{{Text: "for a dead agent"}}
	other.Status = agent.Exited
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(other.Queue) != 1 {
		t.Errorf("the exited agent's queue after the hold: %+v", other.Queue)
	}
	// Back as it was: on the workspace, the queues the hold paused resumed,
	// the one the user paused still paused, the note gone with the hold.
	if m.hold != nil || m.mode != modeWorkspace {
		t.Fatalf("enter did not end the hold: hold %v, mode %d", m.hold, m.mode)
	}
	if s.Paused || other.Paused || !mine.Paused {
		t.Errorf("after the hold: paused %v %v, the user's %v", s.Paused, other.Paused, mine.Paused)
	}
	if !m.awayFrom.Before(time.Now()) || m.awayFrom.IsZero() {
		t.Error("the timeline does not know the user was away")
	}

	// A click ends a hold too, once the note is written.
	m.startHold()
	m.handleMouse(tea.MouseClickMsg{X: 1, Y: 1})
	if m.hold == nil || m.prompt == nil {
		t.Fatal("a click while the note was written ended the hold")
	}
	m.prompt = nil
	m.handleMouse(tea.MouseClickMsg{X: 1, Y: 1})
	if m.hold != nil || m.mode != modeWorkspace {
		t.Errorf("a click did not end the hold: hold %v, mode %d", m.hold, m.mode)
	}
}

func TestWarnAgents(t *testing.T) {
	m, s := historyModel(t)
	s.Status = agent.Working
	for i := range 2 {
		o := session.New(fmt.Sprint("w", i), fmt.Sprint("w", i), m.dir, agent.NewClaude(nil))
		o.Status = agent.Working
		m.sessions = append(m.sessions, o)
	}
	m.askDir(agent.NewClaude(nil), nil)
	if m.confirm == nil || !strings.Contains(m.confirm.question, "4 agents would work at once") {
		t.Fatalf("no warning for a 4th agent: %+v", m.confirm)
	}
	m.confirm.yes()
	if m.prompt == nil {
		t.Error("starting anyway did not ask for the directory")
	}
	m.confirm, m.prompt = nil, nil

	m.askDir(agent.NewGeneric(config.Shell, []string{"sh"}), nil)
	if m.confirm != nil {
		t.Error("a shell was warned of")
	}
	m.prompt = nil
	m.cfg.Health.WarnAgents = 0
	m.askDir(agent.NewClaude(nil), nil)
	if m.confirm != nil {
		t.Error("warn_agents 0 still warned")
	}
}

func TestFileIntent(t *testing.T) {
	m := timelineModel(t, 100, 30)
	id := m.sessions[0].ID
	lines := m.fileIntent(id, "api.go", time.Time{})
	text := strings.Join(lines, "")
	// The diff is headed by the lines as they are, with one blank line under.
	if !strings.HasSuffix(text, "”\n\n") || strings.Count(text, "\n\n") != 1 {
		t.Errorf("blank lines in the intent: %q", text)
	}
	if !strings.Contains(text, "your prompt “fix it”") || !strings.Contains(text, "“done”") || strings.Contains(text, "earlier") {
		t.Errorf("intent = %q", text)
	}
	for _, l := range lines {
		for _, p := range []string{"+", "-", "@@", "diff "} {
			if strings.HasPrefix(l, p) {
				t.Errorf("%q looks like a line of a diff", l)
			}
		}
	}
	// A turn that wrote the file through the shell does not name it in its
	// end: the time the file was written finds it.
	start := time.Now()
	m.history.Record(session.Event{T: start, S: id, K: session.KindTurnStart, V: "cicoria"})
	m.history.Record(session.Event{T: start.Add(time.Minute), S: id, K: session.KindTurnEnd, X: "ran sed"})
	text = strings.Join(m.fileIntent(id, "api.go", start.Add(30*time.Second)), "")
	if !strings.Contains(text, "a message from cicoria") || !strings.Contains(text, "“ran sed”") || !strings.Contains(text, "and 1 earlier turn(s)") {
		t.Errorf("written in the second turn: %q", text)
	}
	// Written after every turn ended: the last turn that named it.
	if text := strings.Join(m.fileIntent(id, "api.go", start.Add(time.Hour)), ""); !strings.Contains(text, "your prompt “fix it”") {
		t.Errorf("written between turns: %q", text)
	}
	if lines := m.fileIntent(id, "other.go", time.Time{}); lines != nil {
		t.Errorf("a file no turn changed has intent %q", lines)
	}
}

func TestUnreadSettles(t *testing.T) {
	m, s := historyModel(t)
	now := time.Now()
	s.Touched["api.go"], s.Touched["done.go"] = now, now
	m.shown, m.trees = m.project(), map[string]*ui.Tree{}
	// A look that failed lists nothing: no file is taken to have left.
	m.applyRefresh(refreshMsg{dir: m.project()})
	if !s.Unread("api.go") || !s.Unread("done.go") {
		t.Error("a failed look cleared the unread files")
	}
	m.applyRefresh(refreshMsg{dir: m.project(), repo: true, changes: []git.Change{{Path: "api.go"}}})
	if !s.Unread("api.go") || s.Unread("done.go") {
		t.Errorf("unread: api.go %v, committed done.go %v", s.Unread("api.go"), s.Unread("done.go"))
	}
	// Narrowed to a commit, nothing is shown unread: nothing can be read there.
	if rows := m.renderChanges(rect{0, 0, 40, 5}); !strings.Contains(ansi.Strip(rows[0]), "1 unread") || !strings.Contains(ansi.Strip(rows[1]), "M•api.go") {
		t.Errorf("unread row:\n%s", ansi.Strip(strings.Join(rows, "\n")))
	}
	m.commit = "abc"
	if rows := ansi.Strip(strings.Join(m.renderChanges(rect{0, 0, 40, 5}), "\n")); strings.Contains(rows, "unread") || strings.Contains(rows, "•") {
		t.Errorf("narrowed to a commit:\n%s", rows)
	}
}

// A file written in a turn counts as the agent's, even through the shell
// and on a line already changed; one written before the turn does not.
func TestWrittenSince(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, text string, at time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(-time.Minute)
	old := start.Add(-time.Hour)
	write("kept.txt", "a\n", old)
	write("edited.txt", "a\n", old)
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	write("edited.txt", "b\n", old)        // changed before the turn
	write("before.txt", "x\n", old)        // new, before the turn
	write("edited.txt", "c\n", time.Now()) // one more character on the changed line, in the turn
	write("new.txt", "y\n", time.Now())

	got := writtenSince(dir, start)
	if len(got) != 2 || got["edited.txt"].IsZero() || got["new.txt"].IsZero() {
		t.Errorf("written in the turn: %v", got)
	}

	m, s := historyModel(t)
	hook := time.Now().Add(time.Hour)
	s.Touched["new.txt"] = hook
	m.applyTouched(touchedMsg{s.ID, got})
	if !s.Touched["new.txt"].Equal(hook) || s.Touched["edited.txt"].IsZero() || !s.Unread("edited.txt") {
		t.Errorf("touched = %v", s.Touched)
	}
}

func TestHealthNear(t *testing.T) {
	r := rule{label: "x", val: 3*60 + 20, raised: 4 * 60, high: 6 * 60, dur: true}
	if !r.near() || healthMark(r) == healthMark(rule{raised: 10}) {
		t.Error("3h20 of 4h is not near")
	}
	if lvl, at, more := r.next(); lvl != levelRaised || at != 4*60 || !more {
		t.Errorf("next = %s at %d, %v", lvl, at, more)
	}
	r.lvl = levelHigh
	if _, _, more := r.next(); more {
		t.Error("high has a next level")
	}
	if row := ansi.Strip(healthRow(r, true)); !strings.Contains(row, "██████████") || !strings.Contains(row, "the highest level") {
		t.Errorf("row at high = %q", row)
	}
}

func TestMeterBar(t *testing.T) {
	for l, want := range map[level]int{levelLow: 0, levelRaised: 5, levelHigh: 10} {
		if n := strings.Count(ansi.Strip(meterBar(l, 10)), "█"); n != want {
			t.Errorf("%s fills %d of 10, want %d", l, n, want)
		}
	}
}

// The stretch on now, known from the keys, joins the history before it.
func TestHealthSumLive(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 24, h, m, 0, 0, time.Local) }
	events := []session.Event{
		{T: at(10, 0), S: "a", K: session.KindPromptTyped},
		{T: at(10, 10), S: "a", K: session.KindPromptTyped},
	}
	h := healthSum(events, statsFrom(0, at(10, 40)), at(10, 40), 1, live{active: 30 * time.Minute})
	if h.longest != 40*time.Minute || h.active != 40*time.Minute || h.dayPeak != 40*time.Minute {
		t.Errorf("with the stretch on now: longest %s, active %s, most in a day %s", h.longest, h.active, h.dayPeak)
	}
}

// Cut to a time, the events keep what tells who was working then.
func TestEventsSince(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 24, h, 0, 0, 0, time.Local) }
	events := []session.Event{
		{T: at(8), K: session.KindRunStart},
		{T: at(8), S: "a", K: session.KindStatus, V: "idle"},
		{T: at(9), S: "a", K: session.KindStatus, V: "working"},
		{T: at(9), S: "a", K: session.KindPromptTyped},
		{T: at(11), S: "a", K: session.KindSwitch},
	}
	got := eventsSince(events, at(10))
	if len(got) != 3 || got[0].K != session.KindRunStart || got[1].V != "working" || got[2].K != session.KindSwitch {
		t.Errorf("events since 10:00 = %+v", got)
	}
	// An agent working since 9:00 still counts at once at 10:30.
	working := []session.Event{{T: at(9), S: "a", K: session.KindStatus, V: "working"}, {T: at(9), S: "b", K: session.KindStatus, V: "working"},
		{T: at(9), S: "c", K: session.KindStatus, V: "working"}, {T: at(9), S: "d", K: session.KindStatus, V: "working"}}
	if rules := healthRules(working, live{}, at(10).Add(30*time.Minute)); rules[2].lvl != levelHigh {
		t.Errorf("4 agents working for the last hour: %+v", rules[2])
	}
	// One of them closed at 9:30 while working, before the day the rules
	// look at, no longer counts the next day.
	closed := append(working, session.Event{T: at(9).Add(30 * time.Minute), S: "d", K: session.KindSessionClose})
	if rules := healthRules(closed, live{}, at(10).Add(24*time.Hour+30*time.Minute)); rules[2].lvl == levelHigh {
		t.Errorf("3 agents working and one closed: %+v", rules[2])
	}
}

// Ended, a hold that stopped the web cockpit starts it again, with what it
// shared then.
func TestHoldRestartsWeb(t *testing.T) {
	m, s := historyModel(t)
	m.cfg.WebAddress = "127.0.0.1:0"
	other := session.New("s2", "docs", m.dir, agent.NewClaude(nil))
	m.sessions = append(m.sessions, other)
	m.startWeb()
	s.Web, other.Web = false, true
	m.startHold()
	m.prompt = nil
	if m.web != nil || other.Web {
		t.Fatal("the hold did not stop the web cockpit")
	}
	m.endHold()
	defer m.stopWeb()
	if m.web == nil || s.Web || !other.Web {
		t.Errorf("after the hold: web %v, shared %v %v", m.web != nil, s.Web, other.Web)
	}
}

func TestHoldWebFails(t *testing.T) {
	m, s := historyModel(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	m.cfg.WebAddress = "127.0.0.1:0"
	m.startWeb()
	s.Web = true
	m.startHold()
	m.prompt = nil
	// The address is taken while the user is away.
	m.cfg.WebAddress = taken.Addr().String()
	m.endHold()
	defer m.stopWeb()
	if m.web != nil || s.Web {
		t.Errorf("after the hold, with the address taken: web %v, shared %v", m.web != nil, s.Web)
	}
}

func TestHoldFromBoard(t *testing.T) {
	m, _ := historyModel(t)
	m.mode = modeBoard
	m.openHealth()
	m.healthKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if m.mode != modeWorkspace || m.hold == nil {
		t.Errorf("hold from the health view of the board: mode %d, hold %v", m.mode, m.hold)
	}
}

func TestHoldAway(t *testing.T) {
	m, _ := historyModel(t)
	left := time.Now().Add(-time.Hour)
	// Back for a while, then a hold: the user left at the hold.
	m.awayFrom, m.activeFrom = left, time.Now().Add(-30*time.Minute)
	m.startHold()
	since := m.hold.since
	m.endHold()
	if !m.awayFrom.Equal(since) {
		t.Errorf("away from %v, want the hold start %v", m.awayFrom, since)
	}
	// A hold right on coming back: the user left before it.
	m.awayFrom, m.activeFrom = left, time.Now().Add(-time.Minute)
	m.startHold()
	m.endHold()
	if !m.awayFrom.Equal(left) {
		t.Errorf("away from %v, want the earlier absence %v", m.awayFrom, left)
	}
}

// TestHoldAwayEnter ends a long hold with enter, as the user does: the enter
// goes through noteKey first, which must not decide the absence.
func TestHoldAwayEnter(t *testing.T) {
	m, _ := historyModel(t)
	now := time.Now()
	since := now.Add(-20 * time.Minute)
	left := since.Add(-time.Hour)
	for _, c := range []struct {
		name   string
		active time.Time
		want   time.Time
	}{
		{"hold right on coming back", since.Add(-time.Minute), left},
		{"back for a while", since.Add(-30 * time.Minute), since},
	} {
		m.awayFrom, m.activeFrom = left, c.active
		m.startHold()
		m.prompt = nil
		m.hold.since = since
		// The z that started the hold is the last key noteKey saw.
		m.lastKey = since
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.hold != nil {
			t.Fatalf("%s: enter did not end the hold", c.name)
		}
		if !m.awayFrom.Equal(c.want) {
			t.Errorf("%s: away from %v, want %v", c.name, m.awayFrom, c.want)
		}
	}
}
