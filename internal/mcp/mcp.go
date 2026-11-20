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

// Package mcp is the MCP server cicoria gives the agents it launches, so that
// an agent can send a message to another session, or ask it a question,
// when the user tells it to.
package mcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/henomis/cicoria/internal/ipc"
)

// Name is the name of the server, as agents show it.
const Name = "cicoria"

// Caller takes a call to the running app and brings back its reply.
type Caller func(ipc.Event) (ipc.Reply, error)

// Serve runs the server on stdin and stdout for the session args name.
func Serve(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	session := fs.String("session", "", "the session the agent runs in")
	socket := fs.String("socket", "", "the socket of the running cicoria")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" || *socket == "" {
		return errors.New("mcp: -session and -socket are required")
	}
	call := func(ev ipc.Event) (ipc.Reply, error) { return ipc.Request(*socket, ev) }
	return New(*session, version, call).Run(ctx, &sdk.StdioTransport{})
}

// Tool arguments.
type (
	listArgs struct{}
	sendArgs struct {
		Session string `json:"session" jsonschema:"the name of the cicoria session to send the message to"`
		Text    string `json:"text" jsonschema:"the message, written as a prompt for the agent of that session"`
	}
	askArgs struct {
		Session  string `json:"session" jsonschema:"the name of the cicoria session to ask"`
		Question string `json:"question" jsonschema:"the question, written as a prompt for the agent of that session"`
		Then     string `json:"then,omitempty" jsonschema:"optional: what you are to do with the answer once it reaches you"`
	}
)

// New returns the server for the session with the given ID, which reaches
// the app through call.
func New(session, version string, call Caller) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: Name, Version: version}, &sdk.ServerOptions{
		Instructions: "cicoria runs several coding agents side by side, each in a named session. " +
			"These tools pass a message or a question to the agent of another session, when the user asks you to.",
	})

	do := func(c ipc.Call) (ipc.Reply, error) {
		r, err := call(ipc.Event{Session: session, Call: &c})
		switch {
		case err != nil:
			return r, fmt.Errorf("cicoria could not be reached: %w", err)
		case !r.OK:
			return r, errors.New(r.Text)
		}
		return r, nil
	}

	sdk.AddTool(s, &sdk.Tool{
		Name:        "list_sessions",
		Description: "List the other cicoria sessions: their name, agent, status and directory, and whether they can be asked a question.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ listArgs) (*sdk.CallToolResult, any, error) {
		r, err := do(ipc.Call{Op: ipc.OpList})
		if err != nil {
			return nil, nil, err
		}
		return text(listText(r.Sessions)), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "send_message",
		Description: "Send a message to the agent of another cicoria session, by the session's name. " +
			"It is queued there and given to that agent when it ends its current turn. No answer comes back: use ask_session for that.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, a sendArgs) (*sdk.CallToolResult, any, error) {
		r, err := do(ipc.Call{Op: ipc.OpSend, To: a.Session, Text: a.Text})
		if err != nil {
			return nil, nil, err
		}
		return text(r.Text), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "ask_session",
		Description: "Ask the agent of another cicoria session a question, by the session's name. " +
			"This returns at once. The answer is what that agent says at the end of the turn the question starts, " +
			"and it reaches you later as a new message, with the question: do not wait or poll for it, end your turn instead.",
	}, func(_ context.Context, _ *sdk.CallToolRequest, a askArgs) (*sdk.CallToolResult, any, error) {
		r, err := do(ipc.Call{Op: ipc.OpAsk, To: a.Session, Text: a.Question, Then: a.Then})
		if err != nil {
			return nil, nil, err
		}
		return text(r.Text), nil, nil
	})
	return s
}

func text(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

// listText describes the sessions, one per line.
func listText(sessions []ipc.SessionInfo) string {
	if len(sessions) == 0 {
		return "There is no other cicoria session."
	}
	var b strings.Builder
	for _, s := range sessions {
		ask := "can be asked"
		if !s.Askable {
			ask = "can only be sent messages"
		}
		fmt.Fprintf(&b, "%s: %s, %s, in %s; %s\n", s.Name, s.Agent, s.Status, s.Dir, ask)
	}
	return strings.TrimRight(b.String(), "\n")
}
