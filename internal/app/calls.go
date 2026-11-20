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
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/ipc"
	"github.com/henomis/cicoria/internal/session"
)

// handleCall does what the agent of a session asked through cicoria's MCP
// server, and returns what to tell it.
func (m *Model) handleCall(id string, c ipc.Call) (ipc.Reply, tea.Cmd) {
	from, _ := m.byID(id)
	if from == nil {
		return refuse("this session is no longer in cicoria"), nil
	}
	if c.Op == ipc.OpList {
		var infos []ipc.SessionInfo
		for _, s := range m.sessions {
			if s == from || s.Adapter.Name() == config.Shell {
				continue
			}
			infos = append(infos, ipc.SessionInfo{
				Name: s.Name, Agent: s.Adapter.Name(), Status: s.Status.String(), Dir: s.Dir,
				Askable: s.Adapter.ReportsStatus() && s.Adapter.ReportsAnswer(),
			})
		}
		return ipc.Reply{OK: true, Sessions: infos}, nil
	}

	to := m.byName(c.To)
	text := strings.TrimSpace(c.Text)
	switch {
	case c.Op != ipc.OpSend && c.Op != ipc.OpAsk:
		return refuse("unknown operation " + c.Op), nil
	case to == nil:
		return refuse(fmt.Sprintf("there is no session named %q: list_sessions names them", c.To)), nil
	case to == from:
		return refuse("that is this session"), nil
	case to.Adapter.Name() == config.Shell:
		// A shell would run the message as commands.
		return refuse(to.Name + " is a shell, not an agent: it takes no messages"), nil
	case text == "":
		return refuse("the message is empty"), nil
	}

	msg := session.Message{Text: text, From: fromAgent, Sender: from.ID}
	if c.Op == ipc.OpAsk {
		// Only an agent that tells what it said has an answer to send back.
		if !to.Adapter.ReportsStatus() || !to.Adapter.ReportsAnswer() {
			return refuse(fmt.Sprintf("%s cannot be asked: %s does not tell what it answered. Send it a message instead", to.Name, to.Adapter.Name())), nil
		}
		msg.ReplyTo, msg.Then, msg.From = from.ID, strings.TrimSpace(c.Then), fromAgentAsk
	}

	verb := "sent a message to "
	if c.Op == ipc.OpAsk {
		verb = "asked "
	}
	m.say(from.Name + "'s agent " + verb + to.Name)
	cmd := m.enqueueFrom(to, msg, session.FromAgent)

	var b strings.Builder
	if c.Op == ipc.OpAsk {
		fmt.Fprintf(&b, "Asked %s. Its answer reaches you as a new message when the turn the question starts ends: end your turn now, do not wait for it.", to.Name)
	} else {
		fmt.Fprintf(&b, "Queued for %s: its agent gets it when it ends its turn.", to.Name)
	}
	if to.Paused {
		b.WriteString(" Its queue is paused, so it waits for the user.")
	}
	return ipc.Reply{OK: true, Text: b.String()}, cmd
}

// byName returns the session with the given name, or nil.
func (m *Model) byName(name string) *session.Session {
	name = strings.TrimSpace(name)
	for _, s := range m.sessions {
		if s.Name == name {
			return s
		}
	}
	for _, s := range m.sessions {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

func refuse(text string) ipc.Reply { return ipc.Reply{Text: text} }
