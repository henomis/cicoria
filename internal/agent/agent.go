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

// Package agent describes how to launch a coding agent and how to read what
// it reports. Everything specific to one agent lives behind Adapter.
package agent

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"

	"github.com/henomis/cicoria/internal/ipc"
)

// Status is what a session is doing, as far as it can be known.
type Status int

// Session statuses.
const (
	Starting Status = iota
	Working
	NeedsInput
	Idle
	Exited
)

func (s Status) String() string {
	switch s {
	case Starting:
		return "starting"
	case Working:
		return "working"
	case NeedsInput:
		return "needs input"
	case Idle:
		return "idle"
	case Exited:
		return "exited"
	}
	return "unknown"
}

// LaunchOpts is what an adapter needs to build the agent's command line.
type LaunchOpts struct {
	// SessionID identifies the session. It is a UUID, stable across restarts.
	SessionID string
	// Dir is the directory the agent works in.
	Dir string
	// Resume is the agent's own name for the conversation to continue, when
	// it reported one that is not SessionID.
	Resume string
	// Self is the path of the cicoria binary, for agents that call it back.
	Self string
	// Socket is where hook events are reported.
	Socket string
	// Port is the first of the ports the session has to itself, or 0 when
	// it has none.
	Port int
}

// EnvPort tells the agent the first of the ports its session has to itself.
const EnvPort = "CICORIA_PORT"

// Update is what an adapter learned from one event.
type Update struct {
	// Status is the new status, when HasStatus is set.
	Status    Status
	HasStatus bool
	// TurnStarted says the agent took a message and begins to answer it.
	TurnStarted bool
	// Prompt is the agent's own name for the message a turn is for, when it
	// tells: the start and the end of one turn have the same.
	Prompt string
	// Interrupted says the user stopped the turn before it had an answer.
	Interrupted bool
	// TurnEnded says the agent finished answering and waits for the next
	// message.
	TurnEnded bool
	// Answer is what the agent said last in the turn that ended, if known.
	Answer string
	// Failed says the turn ended on an error instead of an answer.
	Failed bool
	// Conversation is the agent's own name for the conversation it is in,
	// when it tells. An agent can start a new one in the same process.
	Conversation string
	// File is a file the agent just changed, if any.
	File string
	// Tool is what the agent is about to do, when it is about to use a
	// tool: what a question for permission is about.
	Tool Tool
	// Question is the agent's own words for what it asks, when it reports
	// that it needs input and tells why.
	Question string
}

// Tool is one use of a tool: its name, and the detail that says what it does
// with it, such as the command it runs or the file it writes.
type Tool struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// toolDetails are the inputs that say best what a tool does, in the order
// they are looked for.
var toolDetails = []string{"command", "file_path", "notebook_path", "path", "url", "pattern", "query", "question", "prompt", "description"}

// maxToolDetail bounds the detail of a tool: a command is shown whole, a
// file written is not.
const maxToolDetail = 2000

// toolOf describes the use of a tool from its name and its input, which is
// an object for most tools and a string for some.
func toolOf(name string, input json.RawMessage) Tool {
	t := Tool{Name: name}
	if name == "" {
		return t
	}
	var fields map[string]any
	if json.Unmarshal(input, &fields) == nil {
		for _, k := range toolDetails {
			if v, ok := fields[k].(string); ok && strings.TrimSpace(v) != "" {
				t.Detail = v
				break
			}
		}
	} else {
		var text string
		if json.Unmarshal(input, &text) == nil {
			t.Detail = text
		}
	}
	t.Detail = strings.TrimSpace(t.Detail)
	if r := []rune(t.Detail); len(r) > maxToolDetail {
		t.Detail = string(r[:maxToolDetail]) + "…"
	}
	return t
}

// Adapter knows one kind of agent.
type Adapter interface {
	// Name is how the agent is listed and referred to in saved sessions.
	Name() string
	// Command builds the process to run. Launching a session that ran before
	// continues its conversation when the agent supports that.
	Command(o LaunchOpts) *exec.Cmd
	// ReportsStatus says whether the agent reports its status through events.
	// When it does not, status is guessed from terminal activity.
	ReportsStatus() bool
	// ReportsReady says whether the agent reports that it has started and
	// waits for a message. When it does not, it is taken to be ready once
	// its screen goes quiet.
	ReportsReady() bool
	// ReportsAnswer says whether the agent tells what it said at the end of
	// a turn, which is what the question of another session is answered with.
	ReportsAnswer() bool
	// Mention returns the text to type at the agent's prompt to refer to a
	// file of the project.
	Mention(path string) string
	// MapEvent interprets an event the agent reported.
	MapEvent(ev ipc.Event) (Update, bool)
}

// baseCommand builds a command that runs in the project directory and knows
// which session it belongs to.
func baseCommand(o LaunchOpts, argv []string) *exec.Cmd {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = append(cmd.Environ(),
		ipc.EnvSocket+"="+o.Socket,
		ipc.EnvSession+"="+o.SessionID,
		ipc.EnvSelf+"="+o.Self,
	)
	if o.Port != 0 {
		cmd.Env = append(cmd.Env, EnvPort+"="+strconv.Itoa(o.Port))
	}
	return cmd
}
