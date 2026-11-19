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

package app

import (
	"slices"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/session"
)

// historyModel returns a workspace with one Claude Code session, whose
// history is kept in memory only.
func historyModel(t *testing.T) (*Model, *session.Session) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := session.OpenLedger("", time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{
		cfg:       config.Default(),
		dir:       t.TempDir(),
		history:   l,
		delivered: map[string]time.Time{},
		turnFrom:  map[string]time.Time{},
		answered:  map[string]time.Time{},
	}
	s := session.New("s1", "api", m.dir, agent.NewClaude(nil))
	m.sessions = []*session.Session{s}
	m.track(s)
	return m, s
}

func eventsOf(m *Model, k session.Kind) []session.Event {
	var out []session.Event
	for _, e := range m.history.Events() {
		if e.K == k {
			out = append(out, e)
		}
	}
	return out
}

func TestRecordTurn(t *testing.T) {
	m, s := historyModel(t)
	now := time.Now()

	// A turn nobody sent from cicoria is one the user typed.
	m.recordTurn(s, agent.Update{TurnStarted: true}, now)
	s.Touched["a.go"] = now.Add(time.Second)
	s.Touched["old.go"] = now.Add(-time.Minute)
	m.recordTurn(s, agent.Update{TurnEnded: true, Answer: "done"}, now.Add(2*time.Second))

	if got := eventsOf(m, session.KindPromptTyped); len(got) != 1 {
		t.Errorf("%d typed prompts, want 1", len(got))
	}
	starts := eventsOf(m, session.KindTurnStart)
	if len(starts) != 1 || starts[0].V != "typed" {
		t.Errorf("turn starts = %+v", starts)
	}
	ends := eventsOf(m, session.KindTurnEnd)
	if len(ends) != 1 || !slices.Equal(ends[0].Files, []string{"a.go"}) || ends[0].X != "done" {
		t.Errorf("turn ends = %+v", ends)
	}

	// One cicoria just delivered is not typed.
	m.delivered[s.ID] = now.Add(3 * time.Second)
	m.recordTurn(s, agent.Update{TurnStarted: true}, now.Add(4*time.Second))
	if got := eventsOf(m, session.KindPromptTyped); len(got) != 1 {
		t.Errorf("%d typed prompts after a delivery, want 1", len(got))
	}
	if starts := eventsOf(m, session.KindTurnStart); starts[1].V != "cicoria" {
		t.Errorf("second turn from %q", starts[1].V)
	}
}

func TestStatusIsRecorded(t *testing.T) {
	m, s := historyModel(t)
	s.Apply(agent.Update{HasStatus: true, Status: agent.Working}, true, time.Now())
	s.Apply(agent.Update{HasStatus: true, Status: agent.NeedsInput}, true, time.Now())
	got := eventsOf(m, session.KindStatus)
	if len(got) != 2 || got[0].V != "working" || got[1].V != "needs input" || got[0].S != s.ID {
		t.Errorf("statuses = %+v", got)
	}
}

func TestQueueIsRecorded(t *testing.T) {
	m, s := historyModel(t)
	m.cfg.Timeline.Text = session.TextNone
	m.enqueueFrom(s, session.Message{Text: "secret", From: fromNotes}, session.FromWeb)
	got := eventsOf(m, session.KindQueue)
	if len(got) != 1 || got[0].V != fromNotes || got[0].Src != session.FromWeb || got[0].N != 6 || got[0].X != "" {
		t.Errorf("queued = %+v", got)
	}
}

func TestAway(t *testing.T) {
	m, _ := historyModel(t)
	t0 := time.Now()
	m.noteKey(t0)
	m.noteKey(t0.Add(time.Minute))
	if !m.awayFrom.IsZero() {
		t.Errorf("away from %v after a minute", m.awayFrom)
	}
	m.noteKey(t0.Add(time.Minute + awayAfter))
	if !m.awayFrom.Equal(t0.Add(time.Minute)) {
		t.Errorf("away from %v, want the key before the gap", m.awayFrom)
	}
}
