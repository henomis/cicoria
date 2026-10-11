package session

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/agent"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "sessions.json")

	if st, err := LoadState(path); err != nil || len(st.Sessions) != 0 {
		t.Fatalf("missing file: %+v, %v", st, err)
	}

	want := State{Sessions: []Saved{{ID: "1", Name: "a", Agent: "claude"}, {ID: "2", Name: "b", Agent: "shell"}}, Active: 1}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active != 1 || len(got.Sessions) != 2 || got.Sessions[1] != want.Sessions[1] {
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
	s := New("1", "s", agent.NewClaude(nil))

	// Finishing while the user looks elsewhere is flagged; starting is not.
	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, "/p", false, now)
	if s.Status != agent.Working || s.Unseen {
		t.Errorf("after working: %v unseen=%v", s.Status, s.Unseen)
	}
	s.Apply(agent.Update{Status: agent.NeedsInput, HasStatus: true}, "/p", false, now)
	if s.Status != agent.NeedsInput || !s.Unseen {
		t.Errorf("after needs input: %v unseen=%v", s.Status, s.Unseen)
	}

	// Only files inside the project are remembered, by relative path.
	s.Apply(agent.Update{File: "/p/api/a.go"}, "/p", true, now)
	s.Apply(agent.Update{File: "/etc/passwd"}, "/p", true, now)
	if _, ok := s.Touched["api/a.go"]; !ok || len(s.Touched) != 1 {
		t.Errorf("touched = %v", s.Touched)
	}

	// Nothing revives an exited session.
	s.Exit(true)
	s.Apply(agent.Update{Status: agent.Working, HasStatus: true}, "/p", true, now)
	if s.Status != agent.Exited {
		t.Errorf("status after exit = %v", s.Status)
	}
}
