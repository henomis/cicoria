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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/session"
)

// finish waits for the command a run started and hands its end to the app.
func finish(t *testing.T, m *Model, cmd func() any) *run {
	t.Helper()
	msg, ok := cmd().(runDoneMsg)
	if !ok {
		t.Fatal("the run did not end with runDoneMsg")
	}
	m.runDone(msg.r)
	return msg.r
}

// A command runs in the session's directory with its ports, and what it
// wrote and how it ended are kept.
func TestRun(t *testing.T) {
	m, s := historyModel(t)
	s.Port = 20010
	cmd := m.startRun(s, "pwd; echo port=$CICORIA_PORT; echo oops >&2; exit 3")
	if cmd == nil || len(m.runs) != 1 || m.lastRun == nil {
		t.Fatalf("not started: runs %v", m.runs)
	}
	if !strings.Contains(m.runningStatus(), "▸ pwd;") {
		t.Errorf("status while running = %q", m.runningStatus())
	}
	r := finish(t, m, func() any { return cmd() })

	out, _, done := r.snapshot()
	dir, _ := filepath.EvalSymlinks(s.Dir)
	if !done || !strings.Contains(out, dir) || !strings.Contains(out, "port=20010") || !strings.Contains(out, "oops") {
		t.Errorf("output = %q (done %v)", out, done)
	}
	if r.code != 3 || len(m.runs) != 0 || m.runningStatus() != "" {
		t.Errorf("code %d, runs %v", r.code, m.runs)
	}
	if !strings.Contains(m.message, "exit 3") || !strings.Contains(m.message, "ctrl+a . output") {
		t.Errorf("status = %q", m.message)
	}

	cmd = m.startRun(s, "true")
	finish(t, m, func() any { return cmd() })
	if !strings.Contains(m.message, "✓") || !strings.Contains(m.message, "done") {
		t.Errorf("status = %q", m.message)
	}
}

// A command that waits for input does not wait for it.
func TestRunTakesNoInput(t *testing.T) {
	m, s := historyModel(t)
	cmd := m.startRun(s, "read line; echo got=$line")
	done := make(chan *run)
	go func() { done <- finish(t, m, func() any { return cmd() }) }()
	select {
	case r := <-done:
		if out, _, _ := r.snapshot(); strings.Contains(out, "got=x") {
			t.Errorf("output = %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the command waits for input")
	}
}

// Closing cicoria stops what runs, child processes too.
func TestRunStopped(t *testing.T) {
	m, s := historyModel(t)
	cmd := m.startRun(s, "sleep 30 & sleep 30")
	m.stopRuns()
	start := time.Now()
	r := finish(t, m, func() any { return cmd() })
	if time.Since(start) > 5*time.Second || r.err == nil && r.code == 0 {
		t.Errorf("stopped run: code %d, err %v, after %s", r.code, r.err, time.Since(start))
	}
}

func TestRunOutputTail(t *testing.T) {
	var tl tail
	line := strings.Repeat("x", 99) + "\n"
	for range 2 * maxRunOutput / len(line) {
		tl.write([]byte(line))
	}
	if len(tl.b) > maxRunOutput || !tl.dropped || string(tl.b[:len(line)]) != line {
		t.Errorf("kept %d bytes, dropped %v, starts %q", len(tl.b), tl.dropped, tl.b[:10])
	}
}

// The history has the last command first, once, and is saved with the
// workspace.
func TestRunHistory(t *testing.T) {
	m, _ := historyModel(t)
	for _, l := range []string{"make", "go test ./...", "make"} {
		m.remember(l)
	}
	if want := []string{"make", "go test ./..."}; !slices.Equal(m.runHistory, want) {
		t.Errorf("history = %v, want %v", m.runHistory, want)
	}
	for i := range maxRunHistory + 5 {
		m.remember(strings.Repeat("a", i+1))
	}
	if len(m.runHistory) != maxRunHistory || m.runHistory[0] != strings.Repeat("a", maxRunHistory+5) {
		t.Errorf("history has %d, first %q", len(m.runHistory), m.runHistory[0])
	}

	m.save()
	st, err := session.LoadState(session.StatePath(m.dir))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Runs, m.runHistory) {
		t.Errorf("saved %v", st.Runs)
	}
}

// Up goes back through the history, down comes forward to what was typed.
func TestPromptHistory(t *testing.T) {
	p := &prompt{history: []string{"b", "a"}, hist: -1}
	p.input.Set("x")
	for _, step := range []struct {
		up   bool
		want string
	}{{true, "b"}, {true, "a"}, {true, "a"}, {false, "b"}, {false, "x"}, {false, "x"}} {
		p.step(step.up)
		if got := p.input.Value(); got != step.want {
			t.Fatalf("after up=%v: %q, want %q", step.up, got, step.want)
		}
	}
	// A prompt with no history does nothing.
	q := &prompt{}
	q.input.Set("y")
	q.step(true)
	if q.input.Value() != "y" {
		t.Errorf("got %q", q.input.Value())
	}
}

func TestRunBox(t *testing.T) {
	m, s := historyModel(t)
	m.remember("make build")
	m.askRun()
	if m.prompt == nil || m.prompt.input.Prompt != "run in api: " {
		t.Fatalf("prompt = %+v", m.prompt)
	}
	m.prompt.step(true)
	if m.prompt.input.Value() != "make build" {
		t.Errorf("up gives %q", m.prompt.input.Value())
	}

	// Paths complete from the session's directory, files too.
	if err := os.WriteFile(filepath.Join(s.Dir, "Makefile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.Dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"cat Make": "cat Makefile", "ls scr": "ls scripts/", "ls nope": "ls nope"} {
		if got := m.prompt.complete(in); got != want {
			t.Errorf("complete(%q) = %q, want %q", in, got, want)
		}
	}
}

// The output of the last command opens full screen, and the keys of a file
// do not act on it.
func TestRunOutputView(t *testing.T) {
	m, s := historyModel(t)
	m.width, m.height = 80, 20
	m.openOutput()
	if m.mode == modeView || !strings.Contains(m.message, "nothing was run yet") {
		t.Fatalf("mode %v, status %q", m.mode, m.message)
	}

	cmd := m.startRun(s, "printf 'one\\ntwo\\n'; exit 1")
	finish(t, m, func() any { return cmd() })
	m.openOutput()
	if m.mode != modeView || m.view.output != m.lastRun {
		t.Fatalf("mode %v", m.mode)
	}
	if title := m.view.pager.Title; !strings.Contains(title, "exit 1") || !strings.Contains(title, "api") {
		t.Errorf("title = %q", title)
	}
	if !slices.Equal(m.view.plain, []string{"one", "two"}) {
		t.Errorf("lines = %q", m.view.plain)
	}
	if !m.outputViewKey("c") || m.outputViewKey("j") {
		t.Error("the keys of a file act on the output")
	}

	// Opening a file leaves the output behind.
	m.openFile("Makefile")
	if m.view.output != nil {
		t.Error("a file is shown as output")
	}
}

// Quitting while a command runs asks first.
func TestQuitWithRun(t *testing.T) {
	m, s := historyModel(t)
	m.startRun(s, "sleep 30")
	defer m.stopRuns()
	m.askQuit()
	if m.confirm == nil || !strings.Contains(m.confirm.question, "1 command(s) still running") {
		t.Errorf("confirm = %+v", m.confirm)
	}
}

// While its command runs, the output view follows what it writes, as long
// as the cursor is at the end.
func TestRunOutputFollows(t *testing.T) {
	m, s := historyModel(t)
	m.width, m.height = 80, 10
	cmd := m.startRun(s, "echo a; sleep 0.5; seq 40; sleep 3")
	defer m.stopRuns()
	_ = cmd
	m.openOutput()
	time.Sleep(time.Second)
	m.Update(runTickMsg{m.lastRun})
	p := m.view.pager
	if p.Len() < 41 || p.Cursor != p.Len()-1 {
		t.Errorf("cursor %d of %d lines: the view does not follow", p.Cursor, p.Len())
	}

	// Scrolled up, it stays where it is.
	m.view.pager.Move(-5, m.viewHeight())
	at := m.view.pager.Cursor
	m.Update(runTickMsg{m.lastRun})
	if m.view.pager.Cursor != at {
		t.Errorf("cursor moved from %d to %d", at, m.view.pager.Cursor)
	}
}

// A command that leaves a process behind ended as it says, and what it
// left is stopped with cicoria.
func TestRunLeavesAProcess(t *testing.T) {
	m, s := historyModel(t)
	cmd := m.startRun(s, "sleep 30 & echo started")
	r := finish(t, m, func() any { return cmd() })
	if r.err != nil || r.code != 0 || !strings.Contains(m.message, "✓") {
		t.Fatalf("code %d, err %v, status %q", r.code, r.err, m.message)
	}
	pgid := r.cmd.Process.Pid
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("the process left behind is gone already: %v", err)
	}
	m.stopRuns()
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(-pgid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the process left behind outlives cicoria")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
