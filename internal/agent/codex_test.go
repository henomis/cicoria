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

func TestCodexMapEvent(t *testing.T) {
	working := Update{Status: Working, HasStatus: true}
	tests := []struct {
		payload string
		want    Update
		ok      bool
	}{
		// The first hook to run comes with the first message.
		{`{"hook_event_name":"SessionStart","source":"startup"}`, working, true},
		{`{"hook_event_name":"UserPromptSubmit","prompt":"hi"}`, Update{Status: Working, HasStatus: true, TurnStarted: true}, true},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, working, true},
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm a"}}`, Update{Status: NeedsInput, HasStatus: true}, true},
		// A command may change files, but does not say which.
		{`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo x > a.go"},"tool_response":""}`, working, true},
		// A patch names its files from the directory the agent works in.
		{
			`{"hook_event_name":"PostToolUse","cwd":"/p","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: api/a.go\n@@\n-a\n+b\n*** End Patch"}}`,
			Update{Status: Working, HasStatus: true, File: "/p/api/a.go"}, true,
		},
		{
			`{"hook_event_name":"PostToolUse","cwd":"/p","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Add File: /q/b.go\n+b\n*** End Patch"}}`,
			Update{Status: Working, HasStatus: true, File: "/q/b.go"}, true,
		},
		{`{"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_input":"*** Begin Patch"}`, working, true},
		// A turn the user stopped is over, with no answer to follow up on.
		{`{"hook_event_name":"Interrupt"}`, Update{Status: Idle, HasStatus: true, Interrupted: true}, true},
		{`{"hook_event_name":"Stop","last_assistant_message":"done"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Answer: "done"}, true},
		{`{"hook_event_name":"Stop","session_id":"conv"}`, Update{Status: Idle, HasStatus: true, TurnEnded: true, Conversation: "conv"}, true},
		{`{"hook_event_name":"SessionEnd","reason":"other"}`, Update{}, false},
		{`garbage`, Update{}, false},
	}
	c := NewCodex(nil)
	for _, tt := range tests {
		got, ok := c.MapEvent(ipc.Event{Payload: json.RawMessage(tt.payload)})
		if got != tt.want || ok != tt.ok {
			t.Errorf("MapEvent(%s) = %+v, %v; want %+v, %v", tt.payload, got, ok, tt.want, tt.ok)
		}
	}
}

// codexRollout creates the transcript of a conversation where Codex CLI
// would, and returns its path.
func codexRollout(t *testing.T, conversation string) string {
	t.Helper()
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "10", "12", "rollout-2026-10-12T16-42-40-"+conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexCommand(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	// What the user configured comes first, so that a launcher can end on
	// "--" and pass the rest on.
	c := NewCodex([]string{"ollama", "launch", "codex", "--"})
	o := LaunchOpts{SessionID: "abc", Dir: "/proj", Self: "/bin/cicoria", Socket: "/run/s.sock"}
	cmd := c.Command(o)
	if got := cmd.Args[:4]; !slices.Equal(got, []string{"ollama", "launch", "codex", "--"}) {
		t.Errorf("args = %v", got)
	}
	for _, env := range []string{"CICORIA_SOCK=/run/s.sock", "CICORIA_SESSION=abc", "CICORIA_BIN=/bin/cicoria"} {
		if !slices.Contains(cmd.Env, env) {
			t.Errorf("env lacks %s", env)
		}
	}

	// Every hook is an option of its own, and none names where cicoria is:
	// a command that changed would have to be trusted again.
	if len(cmd.Args) != 4+2*len(codexHookEvents) {
		t.Fatalf("args = %v", cmd.Args)
	}
	for i, name := range codexHookEvents {
		opt, value := cmd.Args[4+2*i], cmd.Args[5+2*i]
		if opt != "-c" || !strings.HasPrefix(value, "hooks."+name+"=") || !strings.Contains(value, `'"$CICORIA_BIN" hook'`) {
			t.Errorf("hook %s is set with %s %s", name, opt, value)
		}
		if strings.Contains(value, "/bin/cicoria") {
			t.Errorf("hook %s names the binary: %s", name, value)
		}
	}

	// Outside of cicoria there is nothing to report to.
	if got := NewCodex(nil).Command(LaunchOpts{SessionID: "abc"}).Args; !slices.Equal(got, []string{"codex"}) {
		t.Errorf("args = %v", got)
	}

	// The conversation Codex CLI told about is continued, as long as it is
	// still there.
	o.Resume = "01a13090-d5d6"
	if got := c.Command(o).Args; slices.Contains(got, "resume") {
		t.Errorf("a conversation that is gone is resumed: %v", got)
	}
	codexRollout(t, "01a13090-d5d6")
	if got := c.Command(o).Args; !slices.Equal(got[len(got)-2:], []string{"resume", "01a13090-d5d6"}) {
		t.Errorf("args end on %v", got[len(got)-2:])
	}
}
