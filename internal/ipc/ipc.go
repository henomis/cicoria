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
	"time"
)

// Environment variables that tell a hook process where to report.
const (
	EnvSocket  = "CICORIA_SOCK"
	EnvSession = "CICORIA_SESSION"
)

// maxEvent bounds the size of a single event.
const maxEvent = 4 << 20

// Event is one hook invocation reported by an agent.
type Event struct {
	// Session is the cicoria session the agent belongs to.
	Session string `json:"session"`
	// Payload is the agent's own hook data, passed through untouched.
	Payload json.RawMessage `json:"payload"`
}

// Server receives events on a unix socket.
type Server struct {
	path   string
	ln     net.Listener
	events chan Event
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

	s := &Server{path: path, ln: ln, events: make(chan Event, 64)}
	go s.accept()
	return s, nil
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			close(s.events)
			return
		}
		go s.read(conn)
	}
}

func (s *Server) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var ev Event
	if err := json.NewDecoder(io.LimitReader(conn, maxEvent)).Decode(&ev); err != nil {
		return
	}
	s.events <- ev
}

// Path returns the socket path, to be handed to agents.
func (s *Server) Path() string { return s.path }

// Events delivers received events. It is closed when the server is.
func (s *Server) Events() <-chan Event { return s.events }

// Close stops the server and removes the socket.
func (s *Server) Close() error {
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

// Forward reads an agent's hook payload from r and reports it to the app
// named by the environment. Outside of a cicoria session it does nothing.
func Forward(r io.Reader) error {
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
	return Send(path, Event{Session: session, Payload: payload})
}
