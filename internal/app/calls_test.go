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
	"strings"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/ipc"
	"github.com/henomis/cicoria/internal/session"
)

// callsModel returns a workspace with the session api of historyModel and
// web, both Claude Code, a Copilot CLI session and a shell.
func callsModel(t *testing.T) (m *Model, api, web, cop, sh *session.Session) {
	t.Helper()
	m, api = historyModel(t)
	web = session.New("s2", "web", m.dir, agent.NewClaude(nil))
	cop = session.New("s3", "cop", m.dir, agent.NewCopilot(nil))
	sh = session.New("s4", "sh", m.dir, agent.NewGeneric("shell", []string{"sh"}))
	for _, s := range []*session.Session{web, cop, sh} {
		m.sessions = append(m.sessions, s)
		m.track(s)
	}
	return m, api, web, cop, sh
}

func TestCallList(t *testing.T) {
	m, _, web, _, _ := callsModel(t)
	r, _ := m.handleCall(web.ID, ipc.Call{Op: ipc.OpList})
	// A shell is no session to message.
	if !r.OK || len(r.Sessions) != 2 {
		t.Fatalf("reply = %+v", r)
	}
	askable := map[string]bool{}
	for _, s := range r.Sessions {
		askable[s.Name] = s.Askable
	}
	if len(askable) != 2 || !askable["api"] || askable["cop"] {
		t.Errorf("askable = %v, want api only", askable)
	}
}

// A message from an agent is queued for the other session, says whose it
// is, and is recorded as such.
func TestCallSend(t *testing.T) {
	m, api, web, _, _ := callsModel(t)
	r, _ := m.handleCall(web.ID, ipc.Call{Op: ipc.OpSend, To: "API", Text: " the port is 8080 "})
	if !r.OK || !strings.Contains(r.Text, "Queued for api") {
		t.Fatalf("reply = %+v", r)
	}
	if len(api.Queue) != 1 {
		t.Fatalf("queue = %+v", api.Queue)
	}
	msg := api.Queue[0]
	if msg.Text != "the port is 8080" || msg.From != fromAgent || msg.Sender != web.ID || msg.ReplyTo != "" {
		t.Errorf("queued = %+v", msg)
	}
	if got := session.AgentPrompt(web.Name, msg); got != "A message from the agent of the cicoria session \"web\":\n\nthe port is 8080" {
		t.Errorf("prompt = %q", got)
	}
	if e := eventsOf(m, session.KindQueue); len(e) != 1 || e[0].V != fromAgent || e[0].To != web.ID || e[0].Src != session.FromAgent {
		t.Errorf("recorded = %+v", e)
	}
}

// A question from an agent is answered back to it, as one from the user is,
// and the answer says the agent asked.
func TestCallAsk(t *testing.T) {
	m, api, web, _, _ := callsModel(t)
	r, _ := m.handleCall(web.ID, ipc.Call{Op: ipc.OpAsk, To: "api", Text: "which port?", Then: "use it"})
	if !r.OK || !strings.Contains(r.Text, "Asked api") {
		t.Fatalf("reply = %+v", r)
	}
	q := api.Queue[0]
	if q.ReplyTo != web.ID || q.Then != "use it" || q.From != fromAgentAsk || q.Sender != web.ID {
		t.Fatalf("queued = %+v", q)
	}
	if !strings.HasPrefix(session.AgentPrompt(web.Name, q), "A question from the agent of the cicoria session \"web\".") {
		t.Errorf("prompt = %q", session.AgentPrompt(web.Name, q))
	}

	// The turn the question starts ends with the answer.
	msg, _ := api.Dequeue()
	api.Ask(msg)
	m.answer(api, agent.Update{TurnStarted: true})
	m.answer(api, agent.Update{TurnEnded: true, Answer: "8080"})
	if len(web.Queue) != 1 {
		t.Fatalf("web queue = %+v", web.Queue)
	}
	want := "You asked the session \"api\":\n> which port?\n\nIt answered:\n> 8080\n\nuse it"
	if got := web.Queue[0]; got.Text != want || got.From != fromAnswer {
		t.Errorf("answer = %+v\nwant text %q", got, want)
	}
}

func TestCallRefused(t *testing.T) {
	m, _, web, _, _ := callsModel(t)
	for _, tc := range []struct {
		from string
		call ipc.Call
		want string
	}{
		{web.ID, ipc.Call{Op: ipc.OpSend, To: "nope", Text: "hi"}, `no session named "nope"`},
		{web.ID, ipc.Call{Op: ipc.OpSend, To: "web", Text: "hi"}, "this session"},
		{web.ID, ipc.Call{Op: ipc.OpSend, To: "api", Text: "  "}, "empty"},
		{web.ID, ipc.Call{Op: ipc.OpAsk, To: "cop", Text: "hi"}, "cop cannot be asked"},
		{web.ID, ipc.Call{Op: ipc.OpAsk, To: "sh", Text: "hi"}, "sh is a shell"},
		{web.ID, ipc.Call{Op: "rm", To: "api", Text: "hi"}, "unknown operation"},
		{"gone", ipc.Call{Op: ipc.OpList}, "no longer in cicoria"},
	} {
		r, _ := m.handleCall(tc.from, tc.call)
		if r.OK || !strings.Contains(r.Text, tc.want) {
			t.Errorf("%+v: reply = %+v, want it to say %q", tc.call, r, tc.want)
		}
	}
	for _, s := range m.sessions {
		if len(s.Queue) != 0 {
			t.Errorf("%s got %+v", s.Name, s.Queue)
		}
	}

}

// A shell would run a message as commands: an agent cannot queue one there.
func TestCallToShellRefused(t *testing.T) {
	m, _, web, _, _ := callsModel(t)
	sh := session.New("s5", "box", m.dir, agent.NewGeneric("shell", []string{"sh"}))
	m.sessions = append(m.sessions, sh)
	r, _ := m.handleCall(web.ID, ipc.Call{Op: ipc.OpSend, To: "box", Text: "rm -rf ~"})
	if r.OK || !strings.Contains(r.Text, "shell") || len(sh.Queue) != 0 {
		t.Errorf("reply = %+v, queue = %+v", r, sh.Queue)
	}
}

// A call that came too late for the agent that made it, which was told it
// failed, does nothing.
func TestExpiredCallDropped(t *testing.T) {
	m, api, web, _, _ := callsModel(t)
	ev := ipc.Event{Session: web.ID, Call: &ipc.Call{Op: ipc.OpSend, To: "api", Text: "hi"}, Deadline: time.Now().Add(-time.Second)}
	m.handleHook(ev)
	if len(api.Queue) != 0 {
		t.Errorf("an expired call was queued: %+v", api.Queue)
	}
	ev.Deadline = time.Now().Add(time.Minute)
	m.handleHook(ev)
	if len(api.Queue) != 1 {
		t.Errorf("a call in time was not queued: %+v", api.Queue)
	}
}

// The timeline says which agent sent a message or asked a question.
func TestAgentCallLines(t *testing.T) {
	m, api, web, _, _ := callsModel(t)
	lines := linesOf(t, m, func(at func(int) time.Time) []session.Event {
		return []session.Event{
			{T: at(1), S: api.ID, K: session.KindQueue, V: fromAgent, To: web.ID, Src: session.FromAgent, X: "hi", N: 2},
			{T: at(2), S: api.ID, K: session.KindQueue, V: fromAgentAsk, To: web.ID, Src: session.FromAgent, X: "port?", N: 5},
		}
	})
	got := texts(lines)
	for _, want := range []string{"got “hi” from the agent of web", "was asked “port?” by the agent of web"} {
		if !strings.Contains(got, want) {
			t.Errorf("lines lack %q:\n%s", want, got)
		}
	}
}
