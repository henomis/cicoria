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

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/henomis/cicoria/internal/ipc"
)

func TestCopilotMapEvent(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	working := Update{Status: Working, HasStatus: true}
	tests := []struct {
		name, payload string
		want          Update
		ok            bool
	}{
		// The first hook to run comes with the first message.
		{"sessionStart", `{"source":"new"}`, working, true},
		{"userPromptSubmitted", `{"prompt":"hi"}`, Update{Status: Working, HasStatus: true, TurnStarted: true}, true},
		{"preToolUse", `{"toolName":"bash","toolArgs":{"command":"ls"}}`, working, true},
		{"preToolUse", `{"toolName":"ask_user","toolArgs":{"question":"which?"}}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{"postToolUse", `{"toolName":"edit","toolArgs":{"path":"/p/a.go","old_str":"a","new_str":"b"}}`, Update{Status: Working, HasStatus: true, File: "/p/a.go"}, true},
		{"postToolUse", `{"toolName":"create","toolArgs":{"path":"/p/b.go"}}`, Update{Status: Working, HasStatus: true, File: "/p/b.go"}, true},
		// A file that was only read is not one the agent changed.
		{"postToolUse", `{"toolName":"view","toolArgs":{"path":"/p/a.go"}}`, working, true},
		{"postToolUse", `{"toolName":"apply_patch","toolArgs":"*** Begin Patch"}`, working, true},
		{"postToolUseFailure", `{"toolName":"edit","toolArgs":{"path":"/p/a.go"}}`, working, true},
		{"notification", `{"notification_type":"permission_prompt","title":"Permission needed"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{"notification", `{"notification_type":"something_else"}`, Update{}, false},
		// An error that is tried again changes nothing; one that is not ends
		// the turn without an answer.
		{"errorOccurred", `{"errorContext":"model_call","recoverable":true}`, Update{}, true},
		{"errorOccurred", `{"errorContext":"model_call","recoverable":false}`, Update{Status: Idle, HasStatus: true, Failed: true}, true},
		{"agentStop", `{"stopReason":"end_turn"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true}, true},
		{"agentStop", `{"sessionId":"conv"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Conversation: "conv"}, true},
		{"sessionEnd", `{"reason":"user_exit"}`, Update{}, false},
		// A hook that does not say its name is not one of Copilot's.
		{"", `{"hook_event_name":"Stop"}`, Update{}, false},
		{"agentStop", `garbage`, Update{}, false},
	}
	c := NewCopilot(nil)
	for _, tt := range tests {
		got, ok := c.MapEvent(ipc.Event{Name: tt.name, Payload: json.RawMessage(tt.payload)})
		if got != tt.want || ok != tt.ok {
			t.Errorf("MapEvent(%s %s) = %+v, %v; want %+v, %v", tt.name, tt.payload, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCopilotCommand(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	// What the user configured comes first, so that a launcher can end on
	// "--" and pass the rest on.
	c := NewCopilot([]string{"ollama", "launch", "copilot", "--"})
	o := LaunchOpts{SessionID: "abc", Dir: "/proj", Self: "/bin/cicoria", Socket: "/run/s.sock"}
	cmd := c.Command(o)
	if got := cmd.Args[:6]; !slices.Equal(got, []string{"ollama", "launch", "copilot", "--", "--session-id", "abc"}) {
		t.Errorf("args = %v", got)
	}
	for _, env := range []string{"CICORIA_SOCK=/run/s.sock", "CICORIA_SESSION=abc", "CICORIA_BIN=/bin/cicoria"} {
		if !slices.Contains(cmd.Env, env) {
			t.Errorf("env lacks %s", env)
		}
	}

	// The hooks come in a plugin, each saying its own name.
	if len(cmd.Args) != 8 || cmd.Args[6] != "--plugin-dir" {
		t.Fatalf("args = %v", cmd.Args)
	}
	plugin := cmd.Args[7]
	if _, err := os.Stat(filepath.Join(plugin, "plugin.json")); err != nil {
		t.Error(err)
	}
	hooks, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range copilotHookEvents {
		if !strings.Contains(string(hooks), `\"$CICORIA_BIN\" hook `+name+`"`) {
			t.Errorf("no hook for %s in %s", name, hooks)
		}
	}

	// Outside of cicoria there is nothing to report to.
	if got := NewCopilot(nil).Command(LaunchOpts{SessionID: "abc"}).Args; !slices.Equal(got, []string{"copilot", "--session-id", "abc"}) {
		t.Errorf("args = %v", got)
	}

	// The conversation Copilot CLI was last in is the one to continue, as
	// long as it is still there.
	o.Resume = "later"
	if got := c.Command(o).Args[5]; got != "abc" {
		t.Errorf("session = %q, want abc", got)
	}
	transcript := copilotTranscript("later")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.Command(o).Args[5]; got != "later" {
		t.Errorf("session = %q, want later", got)
	}
}

func TestCopilotCountsTokens(t *testing.T) {
	t.Setenv("COPILOT_HOME", t.TempDir())
	transcript := copilotTranscript("conv")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(lines ...string) {
		t.Helper()
		f, err := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(u Update, context, in, out int) {
		t.Helper()
		if u.ContextTokens != context || u.InputTokens != in || u.OutputTokens != out {
			t.Errorf("tokens = ctx %d, in %d, out %d; want %d, %d, %d", u.ContextTokens, u.InputTokens, u.OutputTokens, context, in, out)
		}
	}

	write(
		`{"type":"user.message","data":{"content":"hi"}}`,
		`{"type":"session.usage_record","data":{"usage":{"inputTokens":13049,"outputTokens":44,"cacheReadTokens":0}}}`,
		`{"type":"session.usage_record","data":{"usage":{"inputTokens":13113,"outputTokens":80,"cacheReadTokens":13024}}}`,
	)
	// The transcript is the one the hook names, or the one of its session.
	c := NewCopilot(nil)
	u, _ := c.MapEvent(ipc.Event{Name: "agentStop", Payload: json.RawMessage(`{"transcriptPath":"` + transcript + `"}`)})
	check(u, 13113, 26162, 124)

	// Only what was added is read the next time.
	write(`{"type":"session.usage_record","data":{"usage":{"inputTokens":13201,"outputTokens":98}}}`)
	u, _ = c.MapEvent(ipc.Event{Name: "agentStop", Payload: json.RawMessage(`{"sessionId":"conv"}`)})
	check(u, 13201, 39363, 222)

	// What the model said last since the user's message is the answer of
	// the turn; a reply that only calls tools says nothing.
	write(
		`{"type":"assistant.message","data":{"content":"of the turn before"}}`,
		`{"type":"user.message","data":{"content":"next"}}`,
	)
	if u, _ = c.MapEvent(ipc.Event{Name: "agentStop", Payload: json.RawMessage(`{"sessionId":"conv"}`)}); u.Answer != "" {
		t.Errorf("answer of a turn that said nothing = %q", u.Answer)
	}
	write(
		`{"type":"assistant.message","data":{"content":"looking","toolRequests":[{}]}}`,
		`{"type":"assistant.message","data":{"content":"it is 42"}}`,
		`{"type":"assistant.message","data":{"content":"","toolRequests":[{}]}}`,
	)
	if u, _ = c.MapEvent(ipc.Event{Name: "agentStop", Payload: json.RawMessage(`{"sessionId":"conv"}`)}); u.Answer != "it is 42" {
		t.Errorf("answer = %q, want %q", u.Answer, "it is 42")
	}

	// A conversation that is resumed has its count from the first message.
	u, _ = NewCopilot(nil).MapEvent(ipc.Event{Name: "sessionStart", Payload: json.RawMessage(`{"sessionId":"conv"}`)})
	check(u, 13201, 39363, 222)
}
