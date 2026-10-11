// Package session tracks the agents running in the workspace.
package session

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
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
)

// Session is one agent and its terminal.
type Session struct {
	ID      string
	Name    string
	Adapter agent.Adapter
	// Term is the agent's terminal. It is nil until Start succeeds.
	Term *term.Terminal
	// Err is why the agent could not be started, if it could not.
	Err error

	Status agent.Status
	// Unseen marks a status change that happened while the session was not
	// the one on screen.
	Unseen bool
	// ContextTokens is the size of the agent's context, when it reports it.
	ContextTokens int
	// Touched records when the agent last changed each file, by path relative
	// to the project.
	Touched map[string]time.Time

	lastInput  time.Time
	lastOutput time.Time
}

// New returns a session for the given agent that has not been started.
func New(id, name string, a agent.Adapter) *Session {
	return &Session{ID: id, Name: name, Adapter: a, Touched: map[string]time.Time{}}
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

	o.SessionID = s.ID
	t, err := term.Start(s.Adapter.Command(o), width, height)
	s.Err = err
	if err != nil {
		s.Status = agent.Exited
		return err
	}
	s.Term = t
	s.Status = agent.Starting
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
	s.Status = st
}

// Apply records what the agent reported. dir is the project directory and
// active says whether the session is the one on screen.
func (s *Session) Apply(u agent.Update, dir string, active bool, now time.Time) {
	if u.HasStatus && s.Status != agent.Exited {
		s.setStatus(u.Status, active)
	}
	if u.ContextTokens > 0 {
		s.ContextTokens = u.ContextTokens
	}
	if u.File != "" {
		if rel, err := filepath.Rel(dir, u.File); err == nil && filepath.IsLocal(rel) {
			s.Touched[rel] = now
		}
	}
}

// NoteInput records that the user typed into the session.
func (s *Session) NoteInput(now time.Time) {
	s.lastInput = now
	if !s.Adapter.ReportsStatus() && s.Status == agent.NeedsInput {
		s.Status = agent.Idle
	}
}

// NoteOutput records that the agent's screen changed. For agents that do not
// report their status, this is what it is guessed from.
func (s *Session) NoteOutput(active bool, now time.Time) {
	if s.Term == nil || s.Adapter.ReportsStatus() || s.Status == agent.Exited {
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
		return
	}
	switch s.Status {
	case agent.Working:
		if now.Sub(s.lastOutput) > quietAfter {
			s.setStatus(agent.Idle, active)
		}
	case agent.Starting:
		s.Status = agent.Idle
	}
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
