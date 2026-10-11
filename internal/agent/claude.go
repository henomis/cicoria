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
	// tallies holds the token count of each transcript by its path, so that
	// only what is new has to be read. MapEvent is its only user.
	tallies map[string]*tally
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
		// A conversation that is resumed has a count already.
		t := c.count(h.TranscriptPath)
		return Update{Status: Idle, HasStatus: true, ContextTokens: t.context, InputTokens: t.in, OutputTokens: t.out}, true
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
		t := c.count(h.TranscriptPath)
		return Update{
			Status:        Idle,
			HasStatus:     true,
			TurnEnded:     true,
			ContextTokens: t.context,
			InputTokens:   t.in,
			OutputTokens:  t.out,
		}, true
	}
	return Update{}, false
}

// claudeUsage is what one model reply cost, as a transcript records it.
type claudeUsage struct {
	Input         int `json:"input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	Output        int `json:"output_tokens"`
}

func (u claudeUsage) in() int { return u.Input + u.CacheCreation + u.CacheRead }

// tally is the token count of a transcript, as far as it has been read.
type tally struct {
	// offset is where reading goes on from.
	offset int64
	// replies holds the cost of each model reply by its ID. A transcript has
	// one line for every part of a reply, each with the cost of the whole.
	replies map[string]claudeUsage
	// in and out are the tokens read and written by the model over the whole
	// conversation; context is the size of the context at the last reply.
	in, out, context int
}

// read counts what was added to the transcript since the last time. A
// transcript that cannot be read leaves the count as it was.
func (t *tally) read(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	// A transcript that got shorter is another one: count it from the start.
	if st, err := f.Stat(); err != nil || st.Size() < t.offset {
		*t = tally{}
	}
	if t.replies == nil {
		t.replies = map[string]claudeUsage{}
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return
	}
	// A line still being written is left for the next time.
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	t.offset += int64(len(data))

	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Message struct {
				ID    string       `json:"id"`
				Usage *claudeUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Message.Usage == nil {
			continue
		}
		u, id := *entry.Message.Usage, entry.Message.ID
		if id != "" {
			before := t.replies[id]
			t.in, t.out = t.in-before.in(), t.out-before.Output
			t.replies[id] = u
		}
		t.in, t.out, t.context = t.in+u.in(), t.out+u.Output, u.in()
	}
}

// count returns the token count of a transcript, read as far as it goes.
func (c *Claude) count(path string) *tally {
	if c.tallies == nil {
		c.tallies = map[string]*tally{}
	}
	t := c.tallies[path]
	if t == nil {
		t = &tally{}
		c.tallies[path] = t
	}
	t.read(path)
	return t
}
