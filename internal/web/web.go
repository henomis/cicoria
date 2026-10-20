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

// Package web serves the web cockpit: a page, for a phone as much as a
// desktop, that shows the sessions the user shares and passes back what is
// asked of them. It holds no state of its own beyond the last snapshot it
// was given; what is asked comes out of Actions for the workspace to do.
package web

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/henomis/cicoria/internal/git"
)

// Snapshot is what the page shows: the shared sessions of the workspace.
type Snapshot struct {
	Workspace string    `json:"workspace"`
	Sessions  []Session `json:"sessions"`
}

// Session is one shared session.
type Session struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Agent   string `json:"agent"`
	Project string `json:"project"`
	// Status is the session's status as the workspace shows it, and Since
	// when it got into it.
	Status  string    `json:"status"`
	Since   time.Time `json:"since"`
	Stalled bool      `json:"stalled"`
	Queue   []string  `json:"queue"`
	Paused  bool      `json:"paused"`
	// Running says the agent is there to be sent messages and keys.
	Running bool `json:"running"`
	// Answer is what the agent said at the end of its last turn, for the
	// agents that tell.
	Answer string `json:"answer"`
	// Screen is the text of the agent's screen, given only while a page
	// looks at it.
	Screen string `json:"screen,omitempty"`
	// Dir and Base say where the changes are read and what they are counted
	// from: an empty Base is what is not committed.
	Dir  string `json:"-"`
	Base string `json:"-"`
}

// Action is something the page asks of a session.
type Action struct {
	Session string
	// Kind is ActSend, ActQueue or ActKey.
	Kind string
	// Text is the message, or the name of the key.
	Text string
}

// What the page can ask.
const (
	ActSend  = "send"
	ActQueue = "queue"
	ActKey   = "key"
)

// heartbeat is how often an idle event stream is written to, so that a
// connection that is gone is noticed.
const heartbeat = 15 * time.Second

//go:embed page
var page embed.FS

// Server is the running web cockpit.
type Server struct {
	srv     *http.Server
	url     string
	token   string
	local   bool
	actions chan Action
	done    chan struct{}

	mu sync.Mutex
	// snap is the last snapshot, data its JSON, and changed is closed when
	// they are replaced.
	snap    Snapshot
	data    []byte
	changed chan struct{}
	// watching counts the pages looking at the screen of each session.
	watching map[string]int
}

// Start listens on addr and serves the cockpit until Close, behind a
// token of its own.
func Start(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := &Server{
		token:    hex.EncodeToString(b),
		actions:  make(chan Action, 16),
		done:     make(chan struct{}),
		changed:  make(chan struct{}),
		data:     []byte(`{"workspace":"","sessions":[]}`),
		watching: map[string]int{},
	}
	tcp := ln.Addr().(*net.TCPAddr)
	s.url = fmt.Sprintf("http://%s/?t=%s", reachable(tcp), s.token)
	s.local = tcp.IP.IsLoopback()

	mux := http.NewServeMux()
	// The page itself holds nothing: what it shows needs the token.
	static, _ := fs.Sub(page, "page")
	mux.Handle("GET /", noStore(http.FileServerFS(static)))
	mux.Handle("GET /events", s.guard(s.events))
	mux.Handle("GET /api/sessions/{id}/changes", s.guard(s.changes))
	mux.Handle("GET /api/sessions/{id}/diff", s.guard(s.diff))
	mux.Handle("POST /api/sessions/{id}/{kind}", s.guard(s.act))

	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// reachable returns the address to give in the link: the one listened on,
// or, when that is every address of the machine, one another device can
// reach.
func reachable(addr *net.TCPAddr) string {
	ip := addr.IP
	if ip.IsUnspecified() {
		ip = outbound()
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(addr.Port))
}

// outbound returns the address of the machine on the network its traffic
// goes out to, which is where a phone is; a bridge of containers is not. A
// socket that is never written to says which one it is: nothing is sent.
// Without a route out, there is only this machine.
func outbound() net.IP {
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		defer func() { _ = conn.Close() }()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() {
			return a.IP
		}
	}
	return net.IPv4(127, 0, 0, 1)
}

// URL returns the link that opens the cockpit, token included.
func (s *Server) URL() string { return s.url }

// Local reports whether the cockpit listens only on this machine.
func (s *Server) Local() bool { return s.local }

// Actions returns what the page asks, in order.
func (s *Server) Actions() <-chan Action { return s.actions }

// Done is closed when the server is closed.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close stops serving; the token goes with it.
func (s *Server) Close() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	close(s.done)
	return s.srv.Close()
}

// Publish replaces what the page shows. The pages are only told when it
// changed.
func (s *Server) Publish(snap Snapshot) {
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap = snap
	if bytes.Equal(data, s.data) {
		return
	}
	s.data = data
	close(s.changed)
	s.changed = make(chan struct{})
}

// Watching returns the sessions whose screen a page is looking at.
func (s *Server) Watching() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, n := range s.watching {
		if n > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// guard lets through the requests that carry the token: in a header, or
// for the event stream, which cannot send one, in the address. A cookie
// would be sent to every port of the host, and another site cannot read
// the token to send it.
func (s *Server) guard(next http.HandlerFunc) http.Handler {
	return noStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("t")
		if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			token = h
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "this link is not valid any more: open the one cicoria shows", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}))
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// events streams the snapshots to a page, the current one first. A page
// that names a session in watch is also given the text of its screen.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	if id := r.URL.Query().Get("watch"); id != "" {
		s.mu.Lock()
		s.watching[id]++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			if s.watching[id]--; s.watching[id] <= 0 {
				delete(s.watching, id)
			}
			s.mu.Unlock()
		}()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()
	for {
		s.mu.Lock()
		data, changed := s.data, s.changed
		s.mu.Unlock()
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()

		for waiting := true; waiting; {
			select {
			case <-changed:
				waiting = false
			case <-beat.C:
				if _, err := fmt.Fprint(w, ": beat\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-s.done:
				return
			}
		}
	}
}

// shared returns the session of the request, if it is shared.
func (s *Server) shared(r *http.Request) (Session, bool) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.snap.Sessions, func(ss Session) bool { return ss.ID == id })
	if i < 0 {
		return Session{}, false
	}
	return s.snap.Sessions[i], true
}

// change is one changed file as the page gets it.
type change struct {
	Path    string `json:"path"`
	Mark    string `json:"mark"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.shared(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	list, err := git.Status(ss.Dir, ss.Base)
	if err != nil {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	out := []change{}
	for _, c := range list {
		out = append(out, change{c.Path, c.Mark(), c.Added, c.Deleted})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// diff gives the diff of one changed file. Only a file git lists as
// changed is shown, so that nothing else can be read through it.
func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.shared(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	list, err := git.Status(ss.Dir, ss.Base)
	path := r.URL.Query().Get("path")
	i := slices.IndexFunc(list, func(c git.Change) bool { return c.Path == path })
	if err != nil || i < 0 {
		http.NotFound(w, r)
		return
	}
	text, err := git.Diff(ss.Dir, list[i], ss.Base)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}

// act passes what the page asks of a shared session to the workspace.
func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.shared(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a := Action{Session: r.PathValue("id"), Kind: r.PathValue("kind"), Text: body.Text}
	switch a.Kind {
	case ActSend, ActQueue, ActKey:
	default:
		http.NotFound(w, r)
		return
	}
	if a.Text == "" {
		http.Error(w, "nothing to send", http.StatusBadRequest)
		return
	}
	if a.Kind != ActQueue && !ss.Running {
		http.Error(w, "the agent is not running: queue the message instead", http.StatusConflict)
		return
	}
	select {
	case s.actions <- a:
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
	case <-s.done:
		http.Error(w, "cicoria stopped the cockpit", http.StatusServiceUnavailable)
	}
}
