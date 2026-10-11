package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/henomis/cicoria/internal/ipc"
)

// Claude runs Claude Code. It registers hooks, for the launched process only,
// that report what the agent is doing.
type Claude struct {
	argv []string
}

// NewClaude returns the Claude Code adapter. argv is the command line to
// start it with; nil means plain "claude".
func NewClaude(argv []string) *Claude {
	if len(argv) == 0 {
		argv = []string{"claude"}
	}
	return &Claude{argv: argv}
}

// Name implements Adapter.
func (c *Claude) Name() string { return "claude" }

// ReportsStatus implements Adapter.
func (c *Claude) ReportsStatus() bool { return true }

// Mention implements Adapter.
func (c *Claude) Mention(path string) string { return "@" + path + " " }

// claudeHookEvents are the hooks cicoria listens to.
var claudeHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop",
}

// hookSettings returns Claude Code settings that run command on every hook
// cicoria listens to.
func hookSettings(command string) string {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type matcher struct {
		Hooks []hook `json:"hooks"`
	}

	hooks := map[string][]matcher{}
	for _, name := range claudeHookEvents {
		hooks[name] = []matcher{{Hooks: []hook{{Type: "command", Command: command}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	return string(b)
}

// Command implements Adapter.
func (c *Claude) Command(o LaunchOpts) *exec.Cmd {
	argv := append([]string{}, c.argv...)

	// A session that already has a transcript is resumed; otherwise a new one
	// is started under the same ID, so that it can be resumed next time.
	if _, err := os.Stat(claudeTranscript(o.Dir, o.SessionID)); err == nil {
		argv = append(argv, "--resume", o.SessionID)
	} else {
		argv = append(argv, "--session-id", o.SessionID)
	}

	if o.Self != "" && o.Socket != "" {
		argv = append(argv, "--settings", hookSettings(strconv.Quote(o.Self)+" hook"))
	}
	return baseCommand(o, argv)
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeTranscript returns where Claude Code keeps the transcript of a
// session started in dir.
func claudeTranscript(dir, sessionID string) string {
	root := os.Getenv("CLAUDE_CONFIG_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude")
	}
	return filepath.Join(root, "projects", nonAlnum.ReplaceAllString(dir, "-"), sessionID+".jsonl")
}

// claudeHook is the part of a Claude Code hook payload cicoria reads.
type claudeHook struct {
	Event            string `json:"hook_event_name"`
	TranscriptPath   string `json:"transcript_path"`
	NotificationType string `json:"notification_type"`
	ToolInput        struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// MapEvent implements Adapter.
func (c *Claude) MapEvent(ev ipc.Event) (Update, bool) {
	var h claudeHook
	if err := json.Unmarshal(ev.Payload, &h); err != nil {
		return Update{}, false
	}

	switch h.Event {
	case "SessionStart":
		return Update{Status: Idle, HasStatus: true}, true
	case "UserPromptSubmit", "PreToolUse":
		return Update{Status: Working, HasStatus: true}, true
	case "PostToolUse":
		file := h.ToolInput.FilePath
		if file == "" {
			file = h.ToolInput.NotebookPath
		}
		return Update{Status: Working, HasStatus: true, File: file}, true
	case "Notification":
		// An idle reminder is not a request; every other notification means
		// the agent is blocked on the user.
		if h.NotificationType == "idle_prompt" {
			return Update{Status: Idle, HasStatus: true}, true
		}
		return Update{Status: NeedsInput, HasStatus: true}, true
	case "Stop":
		return Update{
			Status:        Idle,
			HasStatus:     true,
			TurnEnded:     true,
			ContextTokens: claudeContextTokens(h.TranscriptPath),
		}, true
	}
	return Update{}, false
}

// claudeContextTokens reads the size of the context from the last model
// reply recorded in a transcript. It returns 0 when it cannot tell.
func claudeContextTokens(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	// The last reply is near the end; there is no need to read it all.
	const tail = 256 << 10
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		_, _ = f.Seek(-tail, io.SeekEnd)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0
	}

	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Message struct {
				Usage *struct {
					Input         int `json:"input_tokens"`
					CacheCreation int `json:"cache_creation_input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &entry) != nil || entry.Message.Usage == nil {
			continue
		}
		u := entry.Message.Usage
		return u.Input + u.CacheCreation + u.CacheRead
	}
	return 0
}
