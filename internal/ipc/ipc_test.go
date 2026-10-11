package ipc

import (
	"strings"
	"testing"
	"time"
)

func TestForwardReachesServer(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	srv, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	t.Setenv(EnvSocket, srv.Path())
	t.Setenv(EnvSession, "s1")
	if err := Forward(strings.NewReader(`{"hook_event_name":"Stop"}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-srv.Events():
		if ev.Session != "s1" || string(ev.Payload) != `{"hook_event_name":"Stop"}` {
			t.Errorf("got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

func TestForwardOutsideSessionIsNoop(t *testing.T) {
	t.Setenv(EnvSocket, "")
	t.Setenv(EnvSession, "")
	if err := Forward(strings.NewReader(`{}`)); err != nil {
		t.Errorf("Forward = %v, want nil", err)
	}
}

func TestForwardRejectsNonJSON(t *testing.T) {
	t.Setenv(EnvSocket, "/nonexistent.sock")
	t.Setenv(EnvSession, "s1")
	if err := Forward(strings.NewReader(`not json`)); err == nil {
		t.Error("Forward accepted a non-JSON payload")
	}
}
