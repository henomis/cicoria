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

// Package session tracks the agents running in the workspace.
package session

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/term"
)

const (
	// echoWindow is how long output after a key press is taken for echo
	// rather than for the agent working.
	echoWindow = 300 * time.Millisecond
	// quietAfter is how long without output until a session counts as idle.
	quietAfter = 1500 * time.Millisecond
	// stallAfter is how long an agent that says it is working can go without
	// a sign of life before that is doubted. A working agent keeps drawing.
	stallAfter = 30 * time.Second
)

// Session is one agent and its terminal.
type Session struct {
	ID   string
	Name string
	// Dir is the directory the agent works in.
	Dir     string
	Adapter agent.Adapter
	// Conversation is the agent's own name for the conversation it is in,
	// when it is not ID: what to continue the next time.
	Conversation string
	// Base is the commit the work tree was on when the session was created:
	// what its changes are counted from. It is empty outside a repository.
	Base string
	// Uncommitted narrows the changes shown to what is not committed. It
	// starts as the settings say.
	Uncommitted bool
	// Term is the agent's terminal. It is nil until Start succeeds.
	Term *term.Terminal
	// Err is why the agent could not be started, if it could not.
	Err error
	// Started is when the agent was last started.
	Started time.Time

	Status agent.Status
	// Since is when the session got into its status.
	Since time.Time
	// Unseen marks a status change that happened while the session was not
	// the one on screen.
	Unseen bool
	// ContextTokens is the size of the agent's context, when it reports it.
	ContextTokens int
	// InputTokens and OutputTokens are what the agent's model read and wrote
	// over the whole conversation, when it reports it.
	InputTokens, OutputTokens int
	// Touched records when the agent last changed each file, by path relative
	// to Dir.
	Touched map[string]time.Time

	// Notes are remarks on lines of code, waiting to be sent to the agent.
	Notes []Note

	// Queue holds messages waiting to be sent to the agent, one per turn.
	Queue []string
	// Paused holds the queue back.
	Paused bool

	lastInput  time.Time
	lastOutput time.Time
}

// Note is a remark of the user on some lines of a file.
type Note struct {
	// Path is the file, relative to the session's directory.
	Path string `json:"path"`
	// From and To are the first and last line the note is about. Removed
	// says they are lines of the file as it was, which the change took away.
	From    int  `json:"from"`
	To      int  `json:"to"`
	Removed bool `json:"removed,omitempty"`
	// Quote is the lines themselves, as they were shown.
	Quote []string `json:"quote,omitempty"`
	Text  string   `json:"text"`
}

// Where names the lines a note is about, as in "api/auth.go:42-44".
func (n Note) Where() string {
	where := fmt.Sprintf("%s:%d", n.Path, n.From)
	if n.To > n.From {
		where += fmt.Sprintf("-%d", n.To)
	}
	if n.Removed {
		where += " (removed)"
	}
	return where
}

// Covers reports whether the note is about a line, given by its number in
// the old and in the new file, of which one may be missing.
func (n Note) Covers(path string, old, cur int) bool {
	line := cur
	if n.Removed {
		// A line that is still there is not one the change took away.
		if cur != 0 {
			return false
		}
		line = old
	}
	return n.Path == path && line >= n.From && line <= n.To && line > 0
}

// Review writes the notes as one message for the agent, file by file.
func Review(notes []Note) string {
	sorted := slices.Clone(notes)
	first := map[string]int{}
	for i, n := range sorted {
		if _, ok := first[n.Path]; !ok {
			first[n.Path] = i
		}
	}
	slices.SortStableFunc(sorted, func(a, b Note) int {
		if a.Path != b.Path {
			return first[a.Path] - first[b.Path]
		}
		return a.From - b.From
	})

	var b strings.Builder
	b.WriteString("Review notes:\n")
	for _, n := range sorted {
		b.WriteString("\n" + n.Where() + "\n")
		for _, q := range n.Quote {
			b.WriteString("> " + q + "\n")
		}
		b.WriteString(n.Text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// New returns a session for the given agent, working in dir, that has not
// been started.
func New(id, name, dir string, a agent.Adapter) *Session {
	return &Session{ID: id, Name: name, Dir: dir, Adapter: a, Touched: map[string]time.Time{}}
}

// NewID returns a random UUID.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Start launches the agent, replacing a terminal left by a previous run.
func (s *Session) Start(o agent.LaunchOpts, width, height int) error {
	if s.Term != nil {
		_ = s.Term.Close()
		s.Term = nil
	}

	o.SessionID, o.Dir, o.Resume = s.ID, s.Dir, s.Conversation
	t, err := term.Start(s.Adapter.Command(o), width, height)
	s.Err = err
	if err != nil {
		s.become(agent.Exited)
		return err
	}
	s.Term = t
	s.Started = time.Now()
	s.become(agent.Starting)
	s.Unseen = false
	return nil
}

// setStatus records a status and whether the user was looking.
func (s *Session) setStatus(st agent.Status, active bool) {
	if st == s.Status {
		return
	}
	// Starting work is not news; stopping or blocking is.
	if !active && st != agent.Working {
		s.Unseen = true
	}
	s.become(st)
}

// become records a status and when it began.
func (s *Session) become(st agent.Status) {
	if st != s.Status {
		s.Status, s.Since = st, time.Now()
	}
}

// Apply records what the agent reported. active says whether the session is
// the one on screen.
func (s *Session) Apply(u agent.Update, active bool, now time.Time) {
	// Word from the agent is a sign of life, like its output.
	s.lastOutput = now
	if u.HasStatus && s.Status != agent.Exited {
		s.setStatus(u.Status, active)
	}
	if u.Conversation != "" && u.Conversation != s.ID {
		s.Conversation = u.Conversation
	}
	if u.ContextTokens > 0 {
		s.ContextTokens = u.ContextTokens
	}
	if u.InputTokens > 0 || u.OutputTokens > 0 {
		s.InputTokens, s.OutputTokens = u.InputTokens, u.OutputTokens
	}
	if u.File != "" {
		if rel, err := filepath.Rel(s.Dir, u.File); err == nil && filepath.IsLocal(rel) {
			s.Touched[rel] = now
		}
	}
}

// NoteInput records that the user typed into the session.
func (s *Session) NoteInput(now time.Time) {
	s.lastInput = now
	if !s.Adapter.ReportsStatus() && s.Status == agent.NeedsInput {
		s.become(agent.Idle)
	}
}

// NoteOutput records that the agent's screen changed. For agents that do not
// report their status, this is what it is guessed from.
func (s *Session) NoteOutput(active bool, now time.Time) {
	if s.Term == nil || s.Status == agent.Exited {
		return
	}
	if s.Adapter.ReportsStatus() {
		// The agent says itself what it does; its output only tells that it
		// is still there.
		s.lastOutput = now
		return
	}
	if s.Term.TakeBell() {
		s.setStatus(agent.NeedsInput, active)
		return
	}
	if now.Sub(s.lastInput) < echoWindow {
		return
	}
	s.lastOutput = now
	if s.Status != agent.NeedsInput {
		s.setStatus(agent.Working, active)
	}
}

// Tick lets time pass: a guessed "working" ends when the output goes quiet.
func (s *Session) Tick(active bool, now time.Time) {
	if s.Adapter.ReportsStatus() {
		// An agent that does not say when it is ready is taken to be once
		// it has drawn its screen.
		if s.Status == agent.Starting && !s.Adapter.ReportsReady() && s.lastOutput.After(s.Started) && now.Sub(s.lastOutput) > quietAfter {
			s.become(agent.Idle)
		}
		return
	}
	switch s.Status {
	case agent.Working:
		if now.Sub(s.lastOutput) > quietAfter {
			s.setStatus(agent.Idle, active)
		}
	case agent.Starting:
		s.become(agent.Idle)
	}
}

// Stalled reports whether an agent that says it is working has been silent
// for so long that it has probably stopped without saying so, as one whose
// turn the user interrupted does.
func (s *Session) Stalled(now time.Time) bool {
	return s.Adapter.ReportsStatus() && s.Status == agent.Working && now.Sub(s.lastOutput) > stallAfter
}

// Enqueue adds a message at the end of the queue.
func (s *Session) Enqueue(text string) {
	s.Queue = append(s.Queue, text)
}

// Dequeue takes the first message off the queue.
func (s *Session) Dequeue() (string, bool) {
	if len(s.Queue) == 0 {
		return "", false
	}
	text := s.Queue[0]
	s.Queue = s.Queue[1:]
	return text, true
}

// RemoveQueued drops message i from the queue.
func (s *Session) RemoveQueued(i int) {
	if i >= 0 && i < len(s.Queue) {
		s.Queue = append(s.Queue[:i:i], s.Queue[i+1:]...)
	}
}

// MoveQueued swaps message i with its neighbour delta places away and
// returns where the message ended up.
func (s *Session) MoveQueued(i, delta int) int {
	j := i + delta
	if i < 0 || i >= len(s.Queue) || j < 0 || j >= len(s.Queue) {
		return i
	}
	s.Queue[i], s.Queue[j] = s.Queue[j], s.Queue[i]
	return j
}

// CanSend reports whether the agent is there to receive a message.
func (s *Session) CanSend() bool {
	return s.Term != nil && s.Status != agent.Exited && len(s.Queue) > 0
}

// CanAutoSend reports whether the next queued message may go out without
// the user asking: only to an agent that says itself that it is idle.
func (s *Session) CanAutoSend() bool {
	return s.CanSend() && !s.Paused && s.Adapter.ReportsStatus() && s.Status == agent.Idle
}

// Sent records that a message was just handed to the agent, which is now
// taken to be working on it.
func (s *Session) Sent(now time.Time) {
	s.lastOutput = now
	s.become(agent.Working)
}

// Exit records that the agent's process ended.
func (s *Session) Exit(active bool) {
	s.setStatus(agent.Exited, active)
}

// Close stops the agent.
func (s *Session) Close() {
	if s.Term != nil {
		_ = s.Term.Close()
	}
}
