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
	"strings"

	"github.com/henomis/cicoria/internal/ipc"
)

// Codex runs OpenAI's Codex CLI. It registers hooks, for the launched
// process only, that report what the agent is doing.
type Codex struct {
	argv []string
	// tallies holds the token count of each transcript. MapEvent is its only
	// user.
	tallies tallies
}

// NewCodex returns the Codex CLI adapter. argv is the command line to start
// it with; nil means plain "codex".
func NewCodex(argv []string) *Codex {
	if len(argv) == 0 {
		argv = []string{"codex"}
	}
	return &Codex{argv: argv}
}

// Name implements Adapter.
func (c *Codex) Name() string { return "codex" }

// ReportsStatus implements Adapter.
func (c *Codex) ReportsStatus() bool { return true }

// ReportsReady implements Adapter. Codex CLI runs no hook until the first
// message is sent.
func (c *Codex) ReportsReady() bool { return false }

// Mention implements Adapter.
func (c *Codex) Mention(path string) string { return "@" + path + " " }

// codexHookEvents are the hooks cicoria listens to.
var codexHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt",
}

// codexHooks returns the options that make Codex CLI run cicoria on every
// hook it listens to. The command is the same wherever cicoria is installed,
// as it finds the binary through the environment: Codex CLI asks the user to
// trust a hook once, and again whenever its command changes.
func codexHooks() []string {
	var argv []string
	for _, name := range codexHookEvents {
		// An interrupted turn waits three seconds for its hooks at most.
		argv = append(argv, "-c", "hooks."+name+`=[{hooks=[{type="command",command='"$`+ipc.EnvSelf+`" hook',timeout=3}]}]`)
	}
	return argv
}

// Command implements Adapter.
func (c *Codex) Command(o LaunchOpts) *exec.Cmd {
	argv := append([]string{}, c.argv...)
	if o.Self != "" && o.Socket != "" {
		argv = append(argv, codexHooks()...)
	}
	// Codex CLI names its conversations itself, and tells the name through
	// the hooks: one that it told is continued.
	if o.Resume != "" && codexTranscript(o.Resume) != "" {
		argv = append(argv, "resume", o.Resume)
	}
	return baseCommand(o, argv)
}

// codexTranscript returns where Codex CLI keeps the transcript of a
// conversation, or nothing when it has none.
func codexTranscript(conversation string) string {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".codex")
	}
	// Transcripts are filed by the day they were started on.
	found, _ := filepath.Glob(filepath.Join(root, "sessions", "*", "*", "*", "rollout-*-"+conversation+".jsonl"))
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

// codexHook is the part of a Codex CLI hook payload cicoria reads.
type codexHook struct {
	Event          string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	LastMessage    string `json:"last_assistant_message"`
	// ToolInput is an object for most tools, but not for all of them.
	ToolInput json.RawMessage `json:"tool_input"`
}

// MapEvent implements Adapter.
func (c *Codex) MapEvent(ev ipc.Event) (Update, bool) {
	var h codexHook
	if err := json.Unmarshal(ev.Payload, &h); err != nil {
		return Update{}, false
	}
	u, ok := c.mapHook(h)
	if ok {
		u.Conversation = h.SessionID
	}
	return u, ok
}

func (c *Codex) mapHook(h codexHook) (Update, bool) {
	switch h.Event {
	case "SessionStart":
		// It runs when the first message is sent, so the agent is at work
		// already. A conversation that is resumed has a count already.
		t := c.tallies.count(h.TranscriptPath, codexLine)
		return Update{Status: Working, HasStatus: true, ContextTokens: t.context, InputTokens: t.in, OutputTokens: t.out}, true
	case "UserPromptSubmit":
		return Update{Status: Working, HasStatus: true, TurnStarted: true}, true
	case "PreToolUse":
		return Update{Status: Working, HasStatus: true}, true
	case "PermissionRequest":
		return Update{Status: NeedsInput, HasStatus: true}, true
	case "PostToolUse":
		return Update{Status: Working, HasStatus: true, File: h.edited()}, true
	case "Interrupt":
		// The user stopped the turn: there is no answer to follow up on, and
		// what waits in the queue stays there.
		return Update{Status: Idle, HasStatus: true, Interrupted: true}, true
	case "Stop":
		t := c.tallies.count(h.TranscriptPath, codexLine)
		return Update{
			Status:        Idle,
			HasStatus:     true,
			TurnEnded:     true,
			Answer:        h.LastMessage,
			ContextTokens: t.context,
			InputTokens:   t.in,
			OutputTokens:  t.out,
		}, true
	}
	return Update{}, false
}

// edited returns the file a tool changed, if it is the one that writes
// files. A patch names each file it touches; the first stands for all.
func (h codexHook) edited() string {
	if h.ToolName != "apply_patch" {
		return ""
	}
	var input struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(h.ToolInput, &input)
	for line := range strings.SplitSeq(input.Command, "\n") {
		for _, mark := range []string{"*** Add File: ", "*** Update File: "} {
			if path, ok := strings.CutPrefix(line, mark); ok {
				path = strings.TrimSpace(path)
				if !filepath.IsAbs(path) {
					path = filepath.Join(h.Cwd, path)
				}
				return path
			}
		}
	}
	return ""
}

// codexLine counts one line of a Codex CLI transcript, which records after
// each model reply what the conversation cost so far. The input counts what
// was cached too.
func codexLine(t *tally, line []byte) {
	type usage struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	}
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
			Info *struct {
				Total usage `json:"total_token_usage"`
				Last  usage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "event_msg" || entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
		return
	}
	info := entry.Payload.Info
	t.in, t.out, t.context = info.Total.Input, info.Total.Output, info.Last.Input
}
