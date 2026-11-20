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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// A run is a command the user ran from the run box, in the directory of a
// session, without opening a shell for it.
type run struct {
	line    string
	dir     string
	session string
	start   time.Time

	cmd *exec.Cmd
	// out holds what the command wrote, both streams, and what follows is
	// set when it ends.
	mu   sync.Mutex
	out  tail
	end  time.Time
	code int
	err  error
	// left says the command ended but left processes running, which
	// still hold its output: they are stopped with cicoria.
	left bool
}

// maxRunOutput is how much of what a command writes is kept: the end.
const maxRunOutput = 1 << 20

// maxRunHistory is how many commands the run box remembers.
const maxRunHistory = 50

// tail keeps the last maxRunOutput bytes written to it.
type tail struct {
	b       []byte
	dropped bool
}

func (t *tail) write(p []byte) {
	t.b = append(t.b, p...)
	if over := len(t.b) - maxRunOutput; over > 0 {
		// Cut at a line, so that the first one kept is whole.
		if i := slices.Index(t.b[over:], '\n'); i >= 0 {
			over += i + 1
		}
		t.b = append(t.b[:0], t.b[over:]...)
		t.dropped = true
	}
}

// runWriter takes the output of a command into its run.
type runWriter struct{ r *run }

func (w runWriter) Write(p []byte) (int, error) {
	w.r.mu.Lock()
	defer w.r.mu.Unlock()
	w.r.out.write(p)
	return len(p), nil
}

// snapshot returns what the command wrote so far, whether its start was
// dropped, and whether it has ended.
func (r *run) snapshot() (out string, dropped, done bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.out.b), r.out.dropped, !r.end.IsZero()
}

// result says how the command ended, and how long it took.
func (r *run) result() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	took := r.end.Sub(r.start).Round(time.Second)
	switch {
	case r.end.IsZero():
		return "running for " + time.Since(r.start).Round(time.Second).String()
	case r.err != nil:
		return r.err.Error()
	case r.code != 0:
		return fmt.Sprintf("exit %d (%s)", r.code, took)
	}
	return fmt.Sprintf("done (%s)", took)
}

// runDoneMsg says a command of the run box ended.
type runDoneMsg struct{ r *run }

// runTickMsg updates the output of a command shown while it runs, in the
// view opened the open-th time.
type runTickMsg struct {
	r    *run
	open int
}

// runShell returns the shell commands run in.
func runShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "sh"
}

// askRun asks for a command to run in the directory of the session on
// screen.
func (m *Model) askRun() tea.Cmd {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	id, dir := s.ID, s.Dir
	p := &prompt{
		input:    ui.Input{Prompt: "run in " + s.Name + ": "},
		complete: func(text string) string { return completeLast(dir, text) },
		history:  m.runHistory,
		hist:     -1,
	}
	p.done = func(line string) tea.Cmd {
		s, _ := m.byID(id)
		if line == "" || s == nil {
			return nil
		}
		return m.startRun(s, line)
	}
	m.prompt = p
	return nil
}

// startRun runs a command line in the directory of a session, with its
// ports, and remembers it.
func (m *Model) startRun(s *session.Session, line string) tea.Cmd {
	m.remember(line)
	r := &run{line: line, dir: s.Dir, session: s.Name, start: time.Now()}
	cmd := exec.Command(runShell(), "-c", line)
	cmd.Dir = s.Dir
	cmd.Env = os.Environ()
	if s.Port != 0 {
		cmd.Env = append(cmd.Env, agent.EnvPort+"="+strconv.Itoa(s.Port))
	}
	// Nothing to type into: a command that asks for input ends instead of
	// waiting for it. Its own process group is stopped as a whole.
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = runWriter{r}, runWriter{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	r.cmd = cmd
	if err := cmd.Start(); err != nil {
		m.say("✕ " + line + ": " + err.Error())
		return nil
	}
	m.runs = append(m.runs, r)
	m.lastRun = r
	m.save()
	return func() tea.Msg {
		err := cmd.Wait()
		r.mu.Lock()
		r.end = time.Now()
		var exit *exec.ExitError
		switch {
		case errors.Is(err, exec.ErrWaitDelay):
			// The shell ended, as it says; what it started in the
			// background goes on.
			r.code, r.left = cmd.ProcessState.ExitCode(), true
		case errors.As(err, &exit):
			r.code = exit.ExitCode()
			if r.code < 0 {
				r.err = errors.New(exit.String())
			}
		case err != nil:
			r.err = err
		}
		r.mu.Unlock()
		return runDoneMsg{r}
	}
}

// runDone says how a command ended, and updates its output if on screen.
func (m *Model) runDone(r *run) tea.Cmd {
	m.runs = slices.DeleteFunc(m.runs, func(x *run) bool { return x == r })
	r.mu.Lock()
	ok, left := r.err == nil && r.code == 0, r.left
	r.mu.Unlock()
	if left {
		m.leftBehind = append(m.leftBehind, r)
	}
	switch {
	case ok && left:
		m.say(ui.Good.Render("✓") + " " + r.line + " " + ui.Dim.Render(r.result()+" · left a process running"))
	case ok:
		m.say(ui.Good.Render("✓") + " " + r.line + " " + ui.Dim.Render(r.result()))
	default:
		m.say(ui.Bad.Render("✕") + " " + r.line + ": " + r.result() + ui.Dim.Render(" · "+m.cfg.Prefix+" . output"))
	}
	if m.mode == modeView && m.view.output == r {
		m.showOutput(r)
	}
	return nil
}

// stopRuns stops the commands still running, and what the ended ones left
// running, as cicoria closes.
func (m *Model) stopRuns() {
	for _, r := range slices.Concat(m.runs, m.leftBehind) {
		if p := r.cmd.Process; p != nil {
			_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
	}
}

// remember puts a command line first in the history of the run box.
func (m *Model) remember(line string) {
	m.runHistory = slices.DeleteFunc(m.runHistory, func(l string) bool { return l == line })
	m.runHistory = append([]string{line}, m.runHistory...)
	if len(m.runHistory) > maxRunHistory {
		m.runHistory = m.runHistory[:maxRunHistory]
	}
}

// openOutput shows what the last command wrote, full screen.
func (m *Model) openOutput() tea.Cmd {
	r := m.lastRun
	if r == nil {
		m.say("nothing was run yet: " + m.cfg.Prefix + " ! runs a command")
		return nil
	}
	m.view.dir, m.view.path, m.view.diff, m.view.commit = r.dir, "", false, nil
	m.view.output = r
	m.view.rendered, m.view.spans = false, nil
	m.view.plain, m.view.refs = nil, nil
	m.view.pager.Fixed, m.view.pager.Wrap = 0, m.view.wrap
	m.mode = modeView
	m.view.outputOpen++
	// Each time it opens, it starts at the end, scrolled to the left.
	m.view.pager.SetContent(nil, nil)
	m.showOutput(r)
	// The end is where a failure is told.
	m.view.pager.Move(m.view.pager.Len(), m.viewHeight())
	return m.tickOutput(r)
}

// showOutput puts the output of a command in the view, keeping its place.
func (m *Model) showOutput(r *run) {
	out, dropped, _ := r.snapshot()
	m.view.pager.Title = r.line + " · " + r.result() + " · " + r.session
	var lines []string
	if dropped {
		lines = append(lines, ui.Dim.Render(fmt.Sprintf("… only the last %d MB are kept", maxRunOutput>>20)))
	}
	text := strings.TrimSuffix(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	switch {
	case text != "":
		lines = append(lines, strings.Split(text, "\n")...)
	case len(lines) == 0:
		lines = append(lines, ui.Dim.Render("no output"))
	}
	// A cursor at the end follows what the command writes; one moved up
	// stays where it is.
	atEnd := m.view.pager.Cursor >= m.view.pager.Len()-1
	m.view.pager.Update(lines)
	m.view.plain = lines
	if atEnd {
		m.view.pager.Move(m.view.pager.Len(), m.viewHeight())
	}
}

// tickOutput updates the output on screen while its command runs.
func (m *Model) tickOutput(r *run) tea.Cmd {
	if _, _, done := r.snapshot(); done {
		return nil
	}
	open := m.view.outputOpen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return runTickMsg{r, open} })
}

// outputViewKey handles the keys that mean something else for a file.
func (m *Model) outputViewKey(key string) bool {
	switch key {
	case "c", "x", "t", "d", "m", "e", "o", "@", "tab", "shift+tab", "v":
		m.say("this is the output of " + m.view.output.line)
	default:
		return false
	}
	return true
}

// runningStatus is what the status bar says of the commands running.
func (m *Model) runningStatus() string {
	switch len(m.runs) {
	case 0:
		return ""
	case 1:
		return ui.Info.Render("▸ " + m.runs[0].line)
	}
	return ui.Info.Render(fmt.Sprintf("▸ %d commands", len(m.runs)))
}

// completeLast completes the last word of a command line as a path from
// dir.
func completeLast(dir, line string) string {
	cut := strings.LastIndex(line, " ") + 1
	return line[:cut] + completePath(dir, line[cut:], true)
}
