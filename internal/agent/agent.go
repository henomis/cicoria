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
	"os/exec"

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
}

// Update is what an adapter learned from one event.
type Update struct {
	// Status is the new status, when HasStatus is set.
	Status    Status
	HasStatus bool
	// TurnEnded says the agent finished answering and waits for the next
	// message.
	TurnEnded bool
	// Failed says the turn ended on an error instead of an answer.
	Failed bool
	// Conversation is the agent's own name for the conversation it is in,
	// when it tells. An agent can start a new one in the same process.
	Conversation string
	// File is a file the agent just changed, if any.
	File string
	// ContextTokens is the size of the agent's context, if known.
	ContextTokens int
	// InputTokens and OutputTokens are what the model read and wrote over
	// the whole conversation, if known.
	InputTokens, OutputTokens int
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
	)
	return cmd
}
