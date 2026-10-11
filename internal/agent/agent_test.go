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
		{`{"hook_event_name":"UserPromptSubmit"}`, Update{Status: Working, HasStatus: true}, true},
		{`{"hook_event_name":"PreToolUse","tool_input":{"file_path":"/p/a.go"}}`, Update{Status: Working, HasStatus: true}, true},
		{`{"hook_event_name":"PostToolUse","tool_input":{"file_path":"/p/a.go"}}`, Update{Status: Working, HasStatus: true, File: "/p/a.go"}, true},
		{`{"hook_event_name":"PostToolUse","tool_input":{"command":"ls"}}`, Update{Status: Working, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{`{"hook_event_name":"Notification"}`, Update{Status: NeedsInput, HasStatus: true}, true},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, Update{Status: Idle, HasStatus: true}, true},
		{`{"hook_event_name":"Stop"}`, Update{Status: Idle, HasStatus: true}, true},
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
}

func TestClaudeContextTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	content := `{"type":"user","message":{"content":"hi"}}
{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":7}}}
{"type":"user","message":{"content":"more"}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := claudeContextTokens(path); got != 3210 {
		t.Errorf("claudeContextTokens = %d, want 3210", got)
	}
	if got := claudeContextTokens(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("missing transcript = %d, want 0", got)
	}
}
