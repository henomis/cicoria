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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func kinds(events []Event) string {
	var b []string
	for _, e := range events {
		s := string(e.K)
		if e.V != "" {
			s += ":" + e.V
		}
		b = append(b, s)
	}
	return strings.Join(b, " ")
}

func TestLedgerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := OpenLedger(path, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{T: t0, K: KindRunStart})
	l.Status("a", "working", false, t0.Add(time.Second))
	l.Record(Event{T: t0.Add(2 * time.Second), S: "a", K: KindTurnEnd, Files: []string{"x.go"}})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	got, err := LoadEvents(path, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); k != "run_start status:working turn_end" {
		t.Errorf("events = %q", k)
	}
	if len(got[2].Files) != 1 || got[2].S != "a" {
		t.Errorf("turn end = %+v", got[2])
	}
	// From a time on, the earlier ones are left out.
	if got, _ := LoadEvents(path, t0.Add(time.Second), 0); len(got) != 2 {
		t.Errorf("since: %d events, want 2", len(got))
	}
}

func TestLoadEventsSkipsBrokenLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	data := `{"t":"2026-11-19T10:00:00Z","k":"run_start"}
not json
{"t":"2026-11-19T10:00:01Z","s":"a","k":"status","v":"idle"}
{"t":"2026-11-19T10:00:02Z","s":"a","k":"sta`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadEvents(path, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); k != "run_start status:idle" {
		t.Errorf("events = %q", k)
	}
}

func TestLoadEventsTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := OpenLedger(path, time.Time{}, 0)
	for i := range 100 {
		l.Record(Event{T: t0.Add(time.Duration(i) * time.Second), S: "a", K: KindSend})
	}
	_ = l.Close()
	info, _ := os.Stat(path)
	got, err := LoadEvents(path, time.Time{}, info.Size()/2)
	if err != nil {
		t.Fatal(err)
	}
	// Half of the file, less the line it was cut in.
	if len(got) < 45 || len(got) > 50 {
		t.Errorf("%d events from half the file", len(got))
	}
	if !got[len(got)-1].T.Equal(t0.Add(99 * time.Second)) {
		t.Errorf("the newest event is not the last: %v", got[len(got)-1].T)
	}
}

func TestCompactLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := OpenLedger(path, time.Time{}, 0)
	for i := range 100 {
		l.Record(Event{T: t0.Add(time.Duration(i) * time.Hour), S: "a", K: KindSend})
	}
	_ = l.Close()
	if err := CompactLedger(path, t0.Add(50*time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadEvents(path, time.Time{}, 0)
	if len(got) != 50 {
		t.Errorf("%d events after compacting by time, want 50", len(got))
	}
	info, _ := os.Stat(path)
	if err := CompactLedger(path, time.Time{}, info.Size()/5); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadEvents(path, time.Time{}, 0)
	if len(got) != 10 || !got[9].T.Equal(t0.Add(99*time.Hour)) {
		t.Errorf("%d events after compacting by size, want the newest 10", len(got))
	}
}

func TestGuessedStatusMerged(t *testing.T) {
	l, _ := OpenLedger("", time.Time{}, 0)
	l.Status("a", "idle", true, t0)
	_ = l.Flush(t0.Add(3 * time.Second))
	// A flicker shorter than minSpan is dropped.
	l.Status("a", "working", true, t0.Add(4*time.Second))
	l.Status("a", "idle", true, t0.Add(5*time.Second))
	l.Status("a", "working", true, t0.Add(10*time.Second))
	_ = l.Flush(t0.Add(11 * time.Second))
	if k := kinds(l.Events()); k != "status:idle status:working" {
		t.Errorf("before it lasted: %q", k)
	}
	_ = l.Flush(t0.Add(13 * time.Second))
	got := l.Events()
	if k := kinds(got); k != "status:idle status:working" {
		t.Errorf("events = %q", k)
	}
	if !got[1].T.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("working from %v, want from when it began to last", got[1].T)
	}
	// A reported status is recorded at once, however short.
	l.Status("b", "working", false, t0)
	l.Status("b", "needs input", false, t0.Add(100*time.Millisecond))
	l.Status("b", "working", false, t0.Add(200*time.Millisecond))
	n := 0
	for _, e := range l.Events() {
		if e.S == "b" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d statuses of b, want 3", n)
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("word ", 100)
	if n, x := Clip(long, TextNone); n != 500 || x != "" {
		t.Errorf("none: %d %q", n, x)
	}
	if _, x := Clip("a\n  b", TextPreview); x != "a b" {
		t.Errorf("preview of lines = %q", x)
	}
	if _, x := Clip(long, TextPreview); len([]rune(x)) != previewLen || !strings.HasSuffix(x, "…") {
		t.Errorf("preview = %d characters", len([]rune(x)))
	}
	if _, x := Clip(long, TextFull); x != long {
		t.Error("full text was cut")
	}
}
