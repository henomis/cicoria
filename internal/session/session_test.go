package session

import (
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/term"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "sessions.json")

	if st, err := LoadState(path); err != nil || len(st.Sessions) != 0 {
		t.Fatalf("missing file: %+v, %v", st, err)
	}

	want := State{Sessions: []Saved{{ID: "1", Name: "a", Agent: "claude"}, {ID: "2", Name: "b", Agent: "shell", Dir: "/work/b", Conversation: "c", Queue: []string{"x", "y"}, Paused: true}}, Active: 1}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active != 1 || len(got.Sessions) != 2 || !reflect.DeepEqual(got.Sessions[1], want.Sessions[1]) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestStatePathDependsOnProject(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	a, b := StatePath("/work/a"), StatePath("/work/b")
	if a == b || filepath.Dir(filepath.Dir(a)) != "/state/cicoria" {
		t.Errorf("paths: %q, %q", a, b)
	}
}

func TestNewID(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, b := NewID(), NewID()
	if !uuid.MatchString(a) || a == b {
		t.Errorf("NewID gave %q then %q", a, b)
	}
}

func TestApply(t *testing.T) {
	now := time.Now()
	s := New("1", "s", "/p", agent.NewClaude(nil))

	// Finishing while the user looks elsewhere is flagged; starting is not.
	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, false, now)
	if s.Status != agent.Working || s.Unseen {
		t.Errorf("after working: %v unseen=%v", s.Status, s.Unseen)
	}
	s.Apply(agent.Update{Status: agent.NeedsInput, HasStatus: true}, false, now)
	if s.Status != agent.NeedsInput || !s.Unseen {
		t.Errorf("after needs input: %v unseen=%v", s.Status, s.Unseen)
	}

	// Only files inside the project are remembered, by relative path.
	s.Apply(agent.Update{File: "/p/api/a.go"}, true, now)
	s.Apply(agent.Update{File: "/etc/passwd"}, true, now)
	if _, ok := s.Touched["api/a.go"]; !ok || len(s.Touched) != 1 {
		t.Errorf("touched = %v", s.Touched)
	}

	// The conversation the agent is in is remembered when it is another one.
	s.Apply(agent.Update{Conversation: "1"}, true, now)
	if s.Conversation != "" {
		t.Errorf("conversation = %q, want none", s.Conversation)
	}
	s.Apply(agent.Update{Conversation: "2"}, true, now)
	if s.Conversation != "2" {
		t.Errorf("conversation = %q, want 2", s.Conversation)
	}

	// Nothing revives an exited session.
	s.Exit(true)
	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, true, now)
	if s.Status != agent.Exited {
		t.Errorf("status after exit = %v", s.Status)
	}
}

func TestStalled(t *testing.T) {
	now := time.Now()
	s := New("1", "s", "/p", agent.NewClaude(nil))
	s.Term = &term.Terminal{}

	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, true, now)
	if s.Stalled(now.Add(stallAfter - time.Second)) {
		t.Error("stalled too soon")
	}
	if !s.Stalled(now.Add(stallAfter + time.Second)) {
		t.Error("not stalled after a long silence")
	}
	// Output, or word from the agent, is a sign of life.
	s.NoteOutput(true, now.Add(20*time.Second))
	if s.Stalled(now.Add(stallAfter+time.Second)) || s.Status != agent.Working {
		t.Errorf("stalled although it drew: %v", s.Status)
	}
	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, true, now.Add(45*time.Second))
	if s.Stalled(now.Add(60 * time.Second)) {
		t.Error("stalled although it reported")
	}
	// Only working can stall, and only for agents that report.
	s.Apply(agent.Update{Status: agent.Idle, HasStatus: true}, true, now)
	if s.Stalled(now.Add(time.Hour)) {
		t.Error("an idle session stalled")
	}
	g := New("2", "g", "/p", agent.NewGeneric("shell", []string{"sh"}))
	g.Status = agent.Working
	if g.Stalled(now.Add(time.Hour)) {
		t.Error("a session with a guessed status stalled")
	}
}

func TestQueue(t *testing.T) {
	s := New("1", "s", "/p", agent.NewClaude(nil))
	for _, text := range []string{"a", "b", "c", "d"} {
		s.Enqueue(text)
	}

	if got := s.MoveQueued(0, 1); got != 1 || s.Queue[0] != "b" || s.Queue[1] != "a" {
		t.Errorf("move down: at %d, queue %v", got, s.Queue)
	}
	if got := s.MoveQueued(0, -1); got != 0 || s.Queue[0] != "b" {
		t.Errorf("move past the start: at %d, queue %v", got, s.Queue)
	}
	s.RemoveQueued(2)
	s.RemoveQueued(9)
	if len(s.Queue) != 3 || s.Queue[2] != "d" {
		t.Errorf("after remove: %v", s.Queue)
	}

	for _, want := range []string{"b", "a", "d"} {
		if got, ok := s.Dequeue(); !ok || got != want {
			t.Errorf("Dequeue = %q, %v; want %q", got, ok, want)
		}
	}
	if _, ok := s.Dequeue(); ok {
		t.Error("Dequeue on an empty queue")
	}
}

func TestCanAutoSend(t *testing.T) {
	// The agent's terminal: any running command will do.
	running := func(a agent.Adapter) *Session {
		s := New("1", "s", t.TempDir(), a)
		s.Adapter = agent.NewGeneric(a.Name(), []string{"sleep", "30"})
		if err := s.Start(agent.LaunchOpts{}, 20, 5); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		s.Adapter = a
		s.Enqueue("next")
		return s
	}

	s := running(agent.NewClaude(nil))
	for status, want := range map[agent.Status]bool{
		agent.Starting:   false,
		agent.Working:    false,
		agent.NeedsInput: false,
		agent.Idle:       true,
		agent.Exited:     false,
	} {
		s.Status = status
		if got := s.CanAutoSend(); got != want {
			t.Errorf("status %v: CanAutoSend = %v, want %v", status, got, want)
		}
	}

	s.Status = agent.Idle
	s.Paused = true
	if s.CanAutoSend() {
		t.Error("a paused queue must not send")
	}
	if !s.CanSend() {
		t.Error("a paused queue can still be sent by hand")
	}
	s.Paused = false
	s.Queue = nil
	if s.CanAutoSend() || s.CanSend() {
		t.Error("nothing to send from an empty queue")
	}

	// An agent whose status is only guessed never gets a message unasked.
	g := running(agent.NewGeneric("shell", []string{"sh"}))
	g.Status = agent.Idle
	if g.CanAutoSend() {
		t.Error("auto-send to an agent that does not report its status")
	}
	if !g.CanSend() {
		t.Error("manual send should be possible")
	}

	g.Sent(time.Now())
	if g.Status != agent.Working {
		t.Errorf("status after Sent = %v", g.Status)
	}
}
