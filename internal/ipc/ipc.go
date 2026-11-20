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

// Package ipc carries events from agent hooks to the running app over a unix
// socket.
package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Environment variables that tell a hook process where to report.
const (
	EnvSocket  = "CICORIA_SOCK"
	EnvSession = "CICORIA_SESSION"
	// EnvSelf is the path of the cicoria binary, for hooks to call.
	EnvSelf = "CICORIA_BIN"
)

// maxEvent bounds the size of a single event.
const maxEvent = 4 << 20

// Event is one hook invocation reported by an agent.
type Event struct {
	// Session is the cicoria session the agent belongs to.
	Session string `json:"session"`
	// Name is the name of the hook, for agents whose payload does not say.
	Name string `json:"name,omitempty"`
	// Payload is the agent's own hook data, passed through untouched.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Call is a request from the session's agent, through cicoria's MCP
	// server, that waits for a Reply. Hooks have none.
	Call *Call `json:"call,omitempty"`

	// Deadline is when the caller of Call stops waiting for the reply; the
	// server sets it. A call taken later must not be done, since its caller
	// was told it failed.
	Deadline time.Time `json:"-"`

	// reply takes the answer to Call back to the connection it came on.
	reply chan Reply
}

// Expired reports whether the event is a call its caller no longer waits for.
func (e Event) Expired(now time.Time) bool {
	return e.Call != nil && !e.Deadline.IsZero() && !now.Before(e.Deadline)
}

// Operations a Call can ask for.
const (
	OpList = "list"
	OpSend = "send"
	OpAsk  = "ask"
)

// Call is what an agent asks cicoria to do for it.
type Call struct {
	// Op is OpList, OpSend or OpAsk.
	Op string `json:"op"`
	// To is the name of the session the message is for.
	To string `json:"to,omitempty"`
	// Text is the message, or the question.
	Text string `json:"text,omitempty"`
	// Then is what the asking session is to do with the answer.
	Then string `json:"then,omitempty"`
}

// Reply is cicoria's answer to a Call.
type Reply struct {
	// OK is false when the call could not be done; Text then says why.
	OK   bool   `json:"ok"`
	Text string `json:"text,omitempty"`
	// Sessions are the other sessions, for OpList.
	Sessions []SessionInfo `json:"sessions,omitempty"`
}

// SessionInfo describes a session to the agent of another one.
type SessionInfo struct {
	Name   string `json:"name"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
	Dir    string `json:"dir"`
	// Askable says whether the session can be asked a question and give
	// its answer back.
	Askable bool `json:"askable"`
}

// Answer replies to the call an event carries. It does nothing for an event
// that carries none, and never blocks.
func (e Event) Answer(r Reply) {
	if e.reply == nil {
		return
	}
	select {
	case e.reply <- r:
	default:
	}
}

// callTimeout bounds how long a call waits for the app to answer it.
const callTimeout = 3 * time.Second

// Server receives events on a unix socket.
type Server struct {
	path   string
	ln     net.Listener
	events chan Event
	// closed is closed with the server, and readers counts the connections
	// still being read: events is closed once there are none.
	closed    chan struct{}
	closeOnce sync.Once
	readers   sync.WaitGroup
	// callTimeout is how long a call waits for the app to answer.
	callTimeout time.Duration
}

// Listen creates the socket in the user's runtime directory.
func Listen() (*Server, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("cicoria-%d.sock", os.Getpid()))
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}

	s := &Server{path: path, ln: ln, events: make(chan Event, 64), closed: make(chan struct{}), callTimeout: callTimeout}
	go s.accept()
	return s, nil
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// An event that is still being read must not find events closed.
			s.readers.Wait()
			close(s.events)
			return
		}
		s.readers.Go(func() { s.read(conn) })
	}
}

func (s *Server) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var ev Event
	if err := json.NewDecoder(io.LimitReader(conn, maxEvent)).Decode(&ev); err != nil {
		return
	}
	if ev.Call != nil {
		ev.reply = make(chan Reply, 1)
		// The app must be done before the caller gives up, with room for
		// the reply to get back.
		ev.Deadline = time.Now().Add(s.callTimeout * 3 / 4)
	}
	// Nobody takes the events of a server that is closed.
	select {
	case s.events <- ev:
	case <-s.closed:
		return
	}
	if ev.Call == nil {
		return
	}

	// A call is answered on the connection it came on, if the app answers in
	// time: otherwise the caller sees the connection close.
	timer := time.NewTimer(s.callTimeout)
	defer timer.Stop()
	select {
	case r := <-ev.reply:
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = json.NewEncoder(conn).Encode(r)
	case <-timer.C:
	case <-s.closed:
	}
}

// Path returns the socket path, to be handed to agents.
func (s *Server) Path() string { return s.path }

// Events delivers received events. It is closed when the server is.
func (s *Server) Events() <-chan Event { return s.events }

// Close stops the server and removes the socket.
func (s *Server) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	err := s.ln.Close()
	// Closing the listener takes the socket away with it, as a rule.
	if rm := os.Remove(s.path); rm != nil && !errors.Is(rm, fs.ErrNotExist) {
		err = errors.Join(err, rm)
	}
	return err
}

// Send reports one event to the server listening on path.
func Send(path string, ev Event) error {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	return json.NewEncoder(conn).Encode(ev)
}

// Request sends a call to the server listening on path and returns its
// reply.
func Request(path string, ev Event) (Reply, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return Reply{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(callTimeout + time.Second))
	if err := json.NewEncoder(conn).Encode(ev); err != nil {
		return Reply{}, err
	}
	var r Reply
	if err := json.NewDecoder(io.LimitReader(conn, maxEvent)).Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return Reply{}, errors.New("cicoria did not answer")
		}
		return Reply{}, err
	}
	return r, nil
}

// Forward reads an agent's hook payload from r and reports it to the app
// named by the environment. name is the name of the hook, when the agent
// gives it on the command line. Outside of a cicoria session it does nothing.
func Forward(name string, r io.Reader) error {
	path, session := os.Getenv(EnvSocket), os.Getenv(EnvSession)
	if path == "" || session == "" {
		return nil
	}

	payload, err := io.ReadAll(io.LimitReader(r, maxEvent))
	if err != nil {
		return err
	}
	if !json.Valid(payload) {
		return errors.New("hook payload is not JSON")
	}
	return Send(path, Event{Session: session, Name: name, Payload: payload})
}
