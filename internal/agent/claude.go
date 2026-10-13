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

// ReportsReady implements Adapter.
func (c *Claude) ReportsReady() bool { return true }

// ReportsAnswer implements Adapter.
func (c *Claude) ReportsAnswer() bool { return true }

// Mention implements Adapter.
func (c *Claude) Mention(path string) string { return "@" + path + " " }

// claudeHookEvents are the hooks cicoria listens to.
var claudeHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure",
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
	// is started under the same ID, so that it can be resumed next time. The
	// conversation Claude Code was last in comes first: /clear starts another
	// one, under an ID of its own.
	resume := ""
	for _, id := range []string{o.Resume, o.SessionID} {
		if _, err := os.Stat(claudeTranscript(o.Dir, id)); id != "" && resume == "" && err == nil {
			resume = id
		}
	}
	if resume != "" {
		argv = append(argv, "--resume", resume)
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
	SessionID        string `json:"session_id"`
	PromptID         string `json:"prompt_id"`
	NotificationType string `json:"notification_type"`
	LastMessage      string `json:"last_assistant_message"`
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
	u, ok := c.mapHook(h)
	if ok {
		u.Conversation, u.Prompt = h.SessionID, h.PromptID
	}
	return u, ok
}

func (c *Claude) mapHook(h claudeHook) (Update, bool) {
	switch h.Event {
	case "SessionStart":
		//
		return Update{Status: Idle, HasStatus: true}, true
	case "UserPromptSubmit":
		return Update{Status: Working, HasStatus: true, TurnStarted: true}, true
	case "PreToolUse":
		return Update{Status: Working, HasStatus: true}, true
	case "PostToolUse":
		file := h.ToolInput.FilePath
		if file == "" {
			file = h.ToolInput.NotebookPath
		}
		return Update{Status: Working, HasStatus: true, File: file}, true
	case "Notification":
		return notification(h.NotificationType)
	case "Stop", "StopFailure":
		// A turn that ended on an error is over, but there is no answer to
		// follow up on: what waits in the queue stays there.
		failed := h.Event == "StopFailure"
		return Update{
			Status:    Idle,
			HasStatus: true,
			TurnEnded: !failed,
			Answer:    h.LastMessage,
			Failed:    failed,
		}, true
	}
	return Update{}, false
}

// notification interprets a notification of the given type, which Claude
// Code and Copilot CLI name alike.
func notification(kind string) (Update, bool) {
	switch kind {
	case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
		return Update{Status: NeedsInput, HasStatus: true}, true
	case "idle_prompt":
		return Update{Status: Idle, HasStatus: true}, true
	}
	// The rest is news, such as a login that went well, and asks for
	// nothing.
	return Update{}, false
}
