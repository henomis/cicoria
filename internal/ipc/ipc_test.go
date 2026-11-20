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

package ipc

import (
	"strings"
	"sync"
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
	if err := Forward("stop", strings.NewReader(`{"hook_event_name":"Stop"}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-srv.Events():
		if ev.Session != "s1" || ev.Name != "stop" || string(ev.Payload) != `{"hook_event_name":"Stop"}` {
			t.Errorf("got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

func TestForwardOutsideSessionIsNoop(t *testing.T) {
	t.Setenv(EnvSocket, "")
	t.Setenv(EnvSession, "")
	if err := Forward("", strings.NewReader(`{}`)); err != nil {
		t.Errorf("Forward = %v, want nil", err)
	}
}

func TestForwardRejectsNonJSON(t *testing.T) {
	t.Setenv(EnvSocket, "/nonexistent.sock")
	t.Setenv(EnvSession, "s1")
	if err := Forward("", strings.NewReader(`not json`)); err == nil {
		t.Error("Forward accepted a non-JSON payload")
	}
}

// Closing the server while events are still coming in drops them, and does
// nothing worse.
func TestCloseWhileEventsArrive(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for range 50 {
		srv, err := Listen()
		if err != nil {
			t.Fatal(err)
		}
		// More than the server holds, and nobody takes them.
		var senders sync.WaitGroup
		for range 2 * cap(srv.events) {
			senders.Go(func() { _ = Send(srv.Path(), Event{Session: "s1", Payload: []byte(`{}`)}) })
		}
		_ = srv.Close()
		senders.Wait()

		closed := make(chan struct{})
		go func() {
			for range srv.Events() {
			}
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("the events of a closed server never ended")
		}
	}
}

// A call reaches the app with what it asks, and the app's answer comes back
// to the caller.
func TestCallGetsReply(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	srv, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	go func() {
		ev := <-srv.Events()
		if ev.Session != "s1" || ev.Call == nil || ev.Call.Op != OpSend || ev.Call.To != "api" || ev.Call.Text != "hi" {
			ev.Answer(Reply{Text: "bad call"})
			return
		}
		ev.Answer(Reply{OK: true, Text: "queued", Sessions: []SessionInfo{{Name: "api"}}})
	}()

	r, err := Request(srv.Path(), Event{Session: "s1", Call: &Call{Op: OpSend, To: "api", Text: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.Text != "queued" || len(r.Sessions) != 1 || r.Sessions[0].Name != "api" {
		t.Errorf("got %+v", r)
	}
}

// A call the app does not answer fails, rather than hanging the agent.
func TestCallWithoutReplyFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	srv, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.callTimeout = 100 * time.Millisecond
	go func() { <-srv.Events() }()

	if _, err := Request(srv.Path(), Event{Session: "s1", Call: &Call{Op: OpList}}); err == nil {
		t.Error("Call succeeded with no reply")
	}
}

// Answering an event that carries no call does nothing.
func TestAnswerWithoutCall(t *testing.T) {
	Event{Session: "s1"}.Answer(Reply{OK: true})
}

// A call the app takes after its caller gave up says so, and one taken in
// time does not.
func TestCallExpires(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	srv, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.callTimeout = 50 * time.Millisecond

	if _, err := Request(srv.Path(), Event{Session: "s1", Call: &Call{Op: OpSend}}); err == nil {
		t.Fatal("Request succeeded with no reply")
	}
	if ev := <-srv.Events(); !ev.Expired(time.Now()) {
		t.Error("a call whose caller gave up has not expired")
	}
	if (Event{Call: &Call{Op: OpSend}, Deadline: time.Now().Add(time.Minute)}).Expired(time.Now()) {
		t.Error("a call in time has expired")
	}
	if (Event{Session: "s1"}).Expired(time.Now()) {
		t.Error("a hook event has expired")
	}
}
