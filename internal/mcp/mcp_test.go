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

package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/henomis/cicoria/internal/ipc"
)

// connect starts the server of session s1 on call and returns a client
// talking to it.
func connect(t *testing.T, call Caller) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	if _, err := New("s1", "test", call).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), r.IsError
}

func TestTools(t *testing.T) {
	cs := connect(t, func(ipc.Event) (ipc.Reply, error) { return ipc.Reply{OK: true}, nil })
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range r.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"ask_session", "list_sessions", "send_message"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

// Each tool makes one call to the app, from the session the server is for,
// and says what the app replied.
func TestCalls(t *testing.T) {
	var got []ipc.Event
	cs := connect(t, func(ev ipc.Event) (ipc.Reply, error) {
		got = append(got, ev)
		if ev.Call.Op == ipc.OpList {
			return ipc.Reply{OK: true, Sessions: []ipc.SessionInfo{
				{Name: "api", Agent: "Claude Code", Status: "idle", Dir: "/src/api", Askable: true},
				{Name: "sh", Agent: "shell", Status: "idle", Dir: "/src"},
			}}, nil
		}
		return ipc.Reply{OK: true, Text: "queued for " + ev.Call.To}, nil
	})

	if out, isErr := callTool(t, cs, "list_sessions", nil); isErr ||
		!strings.Contains(out, "api: Claude Code, idle, in /src/api; can be asked") ||
		!strings.Contains(out, "sh: shell, idle, in /src; can only be sent messages") {
		t.Errorf("list_sessions = %q (error %v)", out, isErr)
	}
	if out, isErr := callTool(t, cs, "send_message", map[string]any{"session": "api", "text": "hi"}); isErr || out != "queued for api" {
		t.Errorf("send_message = %q (error %v)", out, isErr)
	}
	if out, isErr := callTool(t, cs, "ask_session", map[string]any{"session": "api", "question": "port?", "then": "use it"}); isErr || out != "queued for api" {
		t.Errorf("ask_session = %q (error %v)", out, isErr)
	}

	want := []ipc.Call{{Op: ipc.OpList}, {Op: ipc.OpSend, To: "api", Text: "hi"}, {Op: ipc.OpAsk, To: "api", Text: "port?", Then: "use it"}}
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.Session != "s1" || *ev.Call != want[i] {
			t.Errorf("call %d = %+v from %q, want %+v", i, *ev.Call, ev.Session, want[i])
		}
	}
}

// A refusal of the app, or an app that cannot be reached, is a tool error
// that says why.
func TestErrors(t *testing.T) {
	cs := connect(t, func(ev ipc.Event) (ipc.Reply, error) {
		if ev.Call.To == "gone" {
			return ipc.Reply{}, errors.New("connection refused")
		}
		return ipc.Reply{Text: "there is no session named " + ev.Call.To}, nil
	})
	if out, isErr := callTool(t, cs, "send_message", map[string]any{"session": "nope", "text": "hi"}); !isErr || !strings.Contains(out, "no session named nope") {
		t.Errorf("unknown session: %q (error %v)", out, isErr)
	}
	if out, isErr := callTool(t, cs, "send_message", map[string]any{"session": "gone", "text": "hi"}); !isErr || !strings.Contains(out, "could not be reached") {
		t.Errorf("unreachable: %q (error %v)", out, isErr)
	}
}
