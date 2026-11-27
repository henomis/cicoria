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

package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Kind is what an event of the ledger records.
type Kind string

// What the ledger records. Status is what the agent does; the rest are
// turns, and what the user did.
const (
	KindStatus       Kind = "status"
	KindTurnStart    Kind = "turn_start"
	KindTurnEnd      Kind = "turn_end"
	KindTurnFailed   Kind = "turn_failed"
	KindInterrupted  Kind = "interrupted"
	KindAnswer       Kind = "answer"
	KindPromptTyped  Kind = "prompt_typed"
	KindSend         Kind = "send"
	KindQueue        Kind = "queue"
	KindUnqueue      Kind = "unqueue"
	KindWorktreeAdd  Kind = "worktree_add"
	KindWorktreeGone Kind = "worktree_remove"
	KindCardStart    Kind = "card_start"
	KindSessionOpen  Kind = "session_open"
	KindSessionClose Kind = "session_close"
	KindRename       Kind = "rename"
	KindRunStart     Kind = "run_start"
	KindRunEnd       Kind = "run_end"
	// KindSwitch is the user going to another session.
	KindSwitch Kind = "switch"
)

// Where an event came from.
const (
	FromTUI  = "tui"
	FromWeb  = "web"
	FromHook = "hook"
	// FromAgent is the agent of another session, through cicoria's MCP
	// server.
	FromAgent = "agent"
)

// How much of the text the user sends the ledger keeps.
const (
	TextNone    = "none"
	TextPreview = "preview"
	TextFull    = "full"
)

const (
	// previewLen is how much of a text a preview keeps, in characters.
	previewLen = 120
	// minSpan is how long a guessed status has to last to be recorded: an
	// agent whose status is guessed from its output flickers between
	// working and idle.
	minSpan = 2 * time.Second
)

// Event is one thing that happened in a session.
type Event struct {
	T time.Time `json:"t"`
	// S is the ID of the session; run events have none.
	S string `json:"s,omitempty"`
	K Kind   `json:"k"`
	// V is the status, the name, or the label the event is about.
	V string `json:"v,omitempty"`
	// Src is where it came from: FromTUI, FromWeb or FromHook.
	Src string `json:"src,omitempty"`
	// To is the other session, for a question asked from one to another.
	To string `json:"to,omitempty"`
	// N is the length of the text sent, and X what the ledger keeps of it.
	N int    `json:"n,omitempty"`
	X string `json:"x,omitempty"`
	// Files are the files the agent changed in a turn that ended.
	Files []string `json:"files,omitempty"`
	// Agent is the agent a session runs, when it opened.
	Agent string `json:"agent,omitempty"`
}

// EventsPath returns where the ledger of the workspace opened on dir is
// kept: next to its saved sessions.
func EventsPath(dir string) string {
	return filepath.Join(filepath.Dir(StatePath(dir)), "events.jsonl")
}

// Clip returns the length of a text and what of it is kept under mode.
func Clip(text, mode string) (n int, kept string) {
	n = len([]rune(text))
	switch mode {
	case TextFull:
		return n, text
	case TextNone:
		return n, ""
	}
	kept = strings.Join(strings.Fields(text), " ")
	if r := []rune(kept); len(r) > previewLen {
		kept = string(r[:previewLen-1]) + "…"
	}
	return n, kept
}

// Ledger is the history of a workspace: an append-only file of events, and
// the events of it that are in memory.
type Ledger struct {
	path   string
	events []Event
	// unwritten are the events not yet written to the file.
	unwritten []Event
	// pending is a guessed status per session, held until it has lasted
	// long enough to be recorded; last is the status last recorded.
	pending map[string]Event
	last    map[string]string
}

// OpenLedger reads the events of the ledger at path from since on, at most
// the last maxBytes of it. A missing file gives an empty ledger.
func OpenLedger(path string, since time.Time, maxBytes int64) (*Ledger, error) {
	l := &Ledger{path: path, pending: map[string]Event{}, last: map[string]string{}}
	events, err := LoadEvents(path, since, maxBytes)
	l.events = events
	return l, err
}

// Events returns the events in memory, the oldest first, with the guessed
// statuses not yet recorded.
func (l *Ledger) Events() []Event {
	if l == nil {
		return nil
	}
	if len(l.pending) == 0 {
		return l.events
	}
	all := slices.Clone(l.events)
	for _, e := range l.pending {
		all = append(all, e)
	}
	slices.SortStableFunc(all, func(a, b Event) int { return a.T.Compare(b.T) })
	return all
}

// Record adds an event.
func (l *Ledger) Record(e Event) {
	if l == nil {
		return
	}
	if e.T.IsZero() {
		e.T = time.Now()
	}
	e.T = e.T.Truncate(time.Millisecond)
	if e.K == KindStatus {
		l.last[e.S] = e.V
	}
	// A status held until it lasted goes back to its place in time.
	i := len(l.events)
	for i > 0 && l.events[i-1].T.After(e.T) {
		i--
	}
	l.events = slices.Insert(l.events, i, e)
	l.unwritten = append(l.unwritten, e)
}

// Status records that a session got into a status. A guessed one is held
// until it has lasted long enough, and a shorter one is dropped.
func (l *Ledger) Status(id, status string, guessed bool, now time.Time) {
	if l == nil {
		return
	}
	e := Event{T: now.Truncate(time.Millisecond), S: id, K: KindStatus, V: status}
	if p, ok := l.pending[id]; ok {
		delete(l.pending, id)
		if !guessed || now.Sub(p.T) >= minSpan {
			l.Record(p)
		}
	}
	if l.last[id] == status {
		return
	}
	if guessed {
		l.pending[id] = e
		return
	}
	l.Record(e)
}

// Flush records the guessed statuses that lasted long enough, and writes
// what is not written yet.
func (l *Ledger) Flush(now time.Time) error {
	if l == nil {
		return nil
	}
	for id, p := range l.pending {
		if now.Sub(p.T) >= minSpan {
			delete(l.pending, id)
			if l.last[id] != p.V {
				l.Record(p)
			}
		}
	}
	return l.write()
}

// Close records what is held and writes it.
func (l *Ledger) Close() error {
	if l == nil {
		return nil
	}
	for id, p := range l.pending {
		delete(l.pending, id)
		if l.last[id] != p.V {
			l.Record(p)
		}
	}
	return l.write()
}

// write appends the unwritten events to the file, in one write so that two
// cicorias on the same workspace do not mix their lines.
func (l *Ledger) write() error {
	if len(l.unwritten) == 0 || l.path == "" {
		return nil
	}
	var b bytes.Buffer
	for _, e := range l.unwritten {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	l.unwritten = nil
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b.Bytes())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// LoadEvents reads the events of the ledger at path from since on, oldest
// first, from at most its last maxBytes. A line cut short, as by a crash,
// is skipped.
func LoadEvents(path string, since time.Time, maxBytes int64) ([]Event, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	cut := maxBytes > 0 && info.Size() > maxBytes
	if cut {
		if _, err := f.Seek(info.Size()-maxBytes, io.SeekStart); err != nil {
			return nil, err
		}
	}

	var events []Event
	r := bufio.NewReader(f)
	for first := true; ; first = false {
		// A line is read whole, however long the text it keeps.
		line, err := r.ReadBytes('\n')
		// Read from the middle of the file, the first line is a part of one.
		if !first || !cut {
			var e Event
			if json.Unmarshal(line, &e) == nil && e.K != "" && !e.T.Before(since) {
				events = append(events, e)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(events, func(a, b Event) int { return a.T.Compare(b.T) })
	return events, nil
}

// CompactLedger rewrites the ledger at path with only its events from since
// on, and of those only as many of the newest as fit in maxBytes.
func CompactLedger(path string, since time.Time, maxBytes int64) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	events, err := LoadEvents(path, since, 0)
	if err != nil {
		return err
	}
	var lines [][]byte
	size := int64(0)
	for i := len(events) - 1; i >= 0; i-- {
		line, err := json.Marshal(events[i])
		if err != nil {
			continue
		}
		if maxBytes > 0 && size+int64(len(line))+1 > maxBytes {
			break
		}
		size += int64(len(line)) + 1
		lines = append(lines, line)
	}
	slices.Reverse(lines)
	var b bytes.Buffer
	for _, line := range lines {
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
