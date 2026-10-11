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

func TestClaudeCountsTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	write := func(content string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(content); err != nil {
			t.Fatal(err)
		}
	}
	stop := func(c *Claude) Update {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "transcript_path": path})
		u, ok := c.MapEvent(ipc.Event{Payload: payload})
		if !ok || !u.TurnEnded {
			t.Fatalf("Stop gave %+v, %v", u, ok)
		}
		return u
	}
	check := func(u Update, context, in, out int) {
		t.Helper()
		if u.ContextTokens != context || u.InputTokens != in || u.OutputTokens != out {
			t.Errorf("context, in, out = %d, %d, %d; want %d, %d, %d",
				u.ContextTokens, u.InputTokens, u.OutputTokens, context, in, out)
		}
	}

	c := NewClaude(nil)
	check(stop(c), 0, 0, 0)

	// A reply written in two parts is one reply: both lines carry its cost,
	// the later one the final figure.
	write(`{"type":"user","message":{"content":"hi"}}
{"type":"assistant","message":{"id":"a","usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":3}}}
{"type":"assistant","message":{"id":"a","usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":7}}}
`)
	check(stop(c), 3210, 3210, 7)

	// Only what is new is read, and a line still being written waits.
	write(`{"type":"assistant","message":{"id":"b","usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":4000,"output_tokens":20}}}
{"type":"assistant","message":{"id":"c","usage":{"input_tok`)
	check(stop(c), 4001, 7211, 27)
	write(`ens":5,"output_tokens":100}}}
`)
	check(stop(c), 5, 7216, 127)

	// Another cicoria, reading it all at once, counts the same.
	check(stop(NewClaude(nil)), 5, 7216, 127)

	// A transcript that starts over is counted from its start.
	if err := os.WriteFile(path, []byte(`{"message":{"id":"z","usage":{"input_tokens":9,"output_tokens":1}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check(stop(c), 9, 9, 1)
}
