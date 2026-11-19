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

func TestClaudeMapEvent(t *testing.T) {
	tests := []struct {
		payload string
		want    Update
		ok      bool
	}{
		{`{"hook_event_name":"SessionStart"}`, Update{Status: Idle, HasStatus: true}, true},
		{`{"hook_event_name":"UserPromptSubmit"}`, Update{Status: Working, HasStatus: true, TurnStarted: true}, true},
		// The start and the end of a turn name the message it is for.
		{`{"hook_event_name":"UserPromptSubmit","prompt_id":"p1"}`, Update{Status: Working, HasStatus: true, TurnStarted: true, Prompt: "p1"}, true},
		{`{"hook_event_name":"UserPromptSubmit","prompt":"fix it"}`, Update{Status: Working, HasStatus: true, TurnStarted: true, Text: "fix it"}, true},
		{`{"hook_event_name":"Stop","prompt_id":"p1"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Prompt: "p1"}, true},
		{`{"hook_event_name":"PreToolUse","tool_input":{"file_path":"/p/a.go"}}`, Update{Status: Working, HasStatus: true}, true},
		// The tool about to run is what a question for permission is about.
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run the tests"}}`, Update{Status: Working, HasStatus: true, Tool: Tool{Name: "Bash", Detail: "go test ./..."}}, true},
		{`{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/p/a.go","content":"package a"}}`, Update{Status: Working, HasStatus: true, Tool: Tool{Name: "Write", Detail: "/p/a.go"}}, true},
		{`{"hook_event_name":"PreToolUse","tool_name":"TodoWrite","tool_input":{"todos":[]}}`, Update{Status: Working, HasStatus: true, Tool: Tool{Name: "TodoWrite"}}, true},
		{`{"hook_event_name":"PostToolUse","tool_input":{"file_path":"/p/a.go"}}`, Update{Status: Working, HasStatus: true, File: "/p/a.go"}, true},
		{`{"hook_event_name":"PostToolUse","tool_input":{"command":"ls"}}`, Update{Status: Working, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`, Update{Status: NeedsInput, HasStatus: true, Question: "Claude needs your permission to use Bash"}, true},
		{`{"hook_event_name":"Notification","notification_type":"elicitation_dialog"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"agent_needs_input"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, Update{Status: Idle, HasStatus: true}, true},
		// News that asks for nothing changes nothing.
		{`{"hook_event_name":"Notification","notification_type":"auth_success"}`, Update{}, false},
		{`{"hook_event_name":"Notification","notification_type":"quota_auto_resume_fired"}`, Update{}, false},
		{`{"hook_event_name":"Notification"}`, Update{}, false},
		{`{"hook_event_name":"Stop"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true}, true},
		// A turn that failed is over, without being an answer.
		{`{"hook_event_name":"StopFailure"}`, Update{Status: Idle, HasStatus: true, Failed: true}, true},
		{`{"hook_event_name":"Stop","last_assistant_message":"done"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Answer: "done"}, true},
		{`{"hook_event_name":"Stop","session_id":"conv"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Conversation: "conv"}, true},
		{`{"hook_event_name":"SubagentStop"}`, Update{}, false},
		{`garbage`, Update{}, false},
	}
	c := NewClaude(nil)
	for _, tt := range tests {
		got, ok := c.MapEvent(ipc.Event{Payload: json.RawMessage(tt.payload)})
		if got != tt.want || ok != tt.ok {
			t.Errorf("MapEvent(%s) = %+v, %v; want %+v, %v", tt.payload, got, ok, tt.want, tt.ok)
		}
	}
}

func TestClaudeCommand(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	c := NewClaude(nil)
	o := LaunchOpts{SessionID: "abc", Dir: "/work/my.repo", Self: "/bin/cicoria", Socket: "/run/c.sock"}

	cmd := c.Command(o)
	if got := cmd.Args[:3]; !slices.Equal(got, []string{"claude", "--session-id", "abc"}) {
		t.Errorf("new session args = %v", got)
	}
	if cmd.Dir != o.Dir {
		t.Errorf("Dir = %q", cmd.Dir)
	}
	if !slices.Contains(cmd.Env, "CICORIA_SESSION=abc") || !slices.Contains(cmd.Env, "CICORIA_SOCK=/run/c.sock") {
		t.Error("session environment not set")
	}

	// The settings must be valid JSON that wires every hook to "cicoria hook".
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal([]byte(cmd.Args[len(cmd.Args)-1]), &settings); err != nil {
		t.Fatalf("settings are not JSON: %v", err)
	}
	for _, name := range claudeHookEvents {
		h := settings.Hooks[name]
		if len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != `"/bin/cicoria" hook` {
			t.Errorf("hook %s = %+v", name, h)
		}
	}

	// Once a transcript exists, the same session is resumed.
	transcript := claudeTranscript(o.Dir, o.SessionID)
	if !strings.HasSuffix(transcript, filepath.Join("projects", "-work-my-repo", "abc.jsonl")) {
		t.Errorf("transcript path = %q", transcript)
	}
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.Command(o).Args[:3]; !slices.Equal(got, []string{"claude", "--resume", "abc"}) {
		t.Errorf("resumed session args = %v", got)
	}

	// The conversation Claude Code was last in is the one to continue, as
	// long as it left a transcript.
	o.Resume = "later"
	if got := c.Command(o).Args[:3]; !slices.Equal(got, []string{"claude", "--resume", "abc"}) {
		t.Errorf("args with a conversation that has no transcript = %v", got)
	}
	if err := os.WriteFile(claudeTranscript(o.Dir, "later"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = c.Command(o)
	if got := cmd.Args[:3]; !slices.Equal(got, []string{"claude", "--resume", "later"}) {
		t.Errorf("args with a later conversation = %v", got)
	}
	if !slices.Contains(cmd.Env, "CICORIA_SESSION=abc") {
		t.Error("the session lost its own ID")
	}
}

// TestToolOf describes a tool by what it does: the command it runs or the
// file it writes, a string input as it is, and a long one cut.
func TestToolOf(t *testing.T) {
	long := strings.Repeat("x", maxToolDetail+10)
	tests := []struct {
		name, input string
		want        Tool
	}{
		{"Bash", `{"command":"  ls -la  ","description":"List"}`, Tool{Name: "Bash", Detail: "ls -la"}},
		{"Edit", `{"file_path":"/p/a.go","old_string":"a"}`, Tool{Name: "Edit", Detail: "/p/a.go"}},
		{"apply_patch", `"*** Begin Patch"`, Tool{Name: "apply_patch", Detail: "*** Begin Patch"}},
		{"Bash", `{"command":"` + long + `"}`, Tool{Name: "Bash", Detail: long[:maxToolDetail] + "…"}},
		{"", `{"command":"ls"}`, Tool{}},
	}
	for _, tt := range tests {
		if got := toolOf(tt.name, json.RawMessage(tt.input)); got != tt.want {
			t.Errorf("toolOf(%q, %s) = %+v, want %+v", tt.name, tt.input[:min(len(tt.input), 40)], got, tt.want)
		}
	}
}
