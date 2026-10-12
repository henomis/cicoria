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
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/henomis/cicoria/internal/ipc"
)

// Copilot runs GitHub Copilot CLI. It loads a plugin, for the launched
// process only, whose hooks report what the agent is doing.
type Copilot struct {
	argv []string
	// tallies holds the token count of each transcript. MapEvent is its only
	// user.
	tallies tallies
}

// NewCopilot returns the Copilot CLI adapter. argv is the command line to
// start it with; nil means plain "copilot".
func NewCopilot(argv []string) *Copilot {
	if len(argv) == 0 {
		argv = []string{"copilot"}
	}
	return &Copilot{argv: argv}
}

// Name implements Adapter.
func (c *Copilot) Name() string { return "copilot" }

// ReportsStatus implements Adapter.
func (c *Copilot) ReportsStatus() bool { return true }

// ReportsReady implements Adapter. Copilot CLI runs no hook until the first
// message is sent.
func (c *Copilot) ReportsReady() bool { return false }

// Mention implements Adapter.
func (c *Copilot) Mention(path string) string { return "@" + path + " " }

// copilotHookEvents are the hooks cicoria listens to.
var copilotHookEvents = []string{
	"sessionStart", "userPromptSubmitted", "preToolUse", "postToolUse", "postToolUseFailure", "notification", "errorOccurred", "agentStop",
}

// copilotHooks returns the hooks file of a Copilot CLI plugin that reports
// every hook cicoria listens to. A payload does not say which hook it is
// from, so the command does.
func copilotHooks() []byte {
	type hook struct {
		Type    string `json:"type"`
		Bash    string `json:"bash"`
		Timeout int    `json:"timeoutSec"`
	}

	hooks := map[string][]hook{}
	for _, name := range copilotHookEvents {
		hooks[name] = []hook{{Type: "command", Bash: `"$` + ipc.EnvSelf + `" hook ` + name, Timeout: 5}}
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "hooks": hooks})
	return b
}

// copilotPlugin writes the plugin that carries cicoria's hooks, unless it is
// there already, and returns its directory. It is the same for every
// session: the hooks find cicoria through the environment.
func copilotPlugin() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "cicoria", "copilot")
	files := map[string][]byte{
		"plugin.json":      []byte(`{"name":"cicoria","description":"Reports what the agent is doing to cicoria.","version":"1.0.0"}`),
		"hooks/hooks.json": copilotHooks(),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Command implements Adapter.
func (c *Copilot) Command(o LaunchOpts) *exec.Cmd {
	argv := append([]string{}, c.argv...)

	// One option both starts a session under an ID and continues the one
	// that has it. The conversation Copilot CLI was last in comes first:
	// /clear starts another one, under an ID of its own.
	id := o.SessionID
	if _, err := os.Stat(copilotTranscript(o.Resume)); o.Resume != "" && err == nil {
		id = o.Resume
	}
	argv = append(argv, "--session-id", id)

	// Without the plugin the agent still runs, though it reports nothing.
	if o.Self != "" && o.Socket != "" {
		if dir, err := copilotPlugin(); err == nil {
			argv = append(argv, "--plugin-dir", dir)
		}
	}
	return baseCommand(o, argv)
}

// copilotTranscript returns where Copilot CLI keeps the transcript of a
// session.
func copilotTranscript(sessionID string) string {
	root := os.Getenv("COPILOT_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".copilot")
	}
	return filepath.Join(root, "session-state", sessionID, "events.jsonl")
}

// copilotHook is the part of a Copilot CLI hook payload cicoria reads.
type copilotHook struct {
	SessionID        string `json:"sessionId"`
	TranscriptPath   string `json:"transcriptPath"`
	NotificationType string `json:"notification_type"`
	ToolName         string `json:"toolName"`
	// ToolArgs is an object for most tools, but not for all of them.
	ToolArgs    json.RawMessage `json:"toolArgs"`
	Recoverable *bool           `json:"recoverable"`
}

// MapEvent implements Adapter.
func (c *Copilot) MapEvent(ev ipc.Event) (Update, bool) {
	var h copilotHook
	if err := json.Unmarshal(ev.Payload, &h); err != nil {
		return Update{}, false
	}
	u, ok := c.mapHook(ev.Name, h)
	if ok {
		u.Conversation = h.SessionID
	}
	return u, ok
}

func (c *Copilot) mapHook(name string, h copilotHook) (Update, bool) {
	switch name {
	case "sessionStart":
		// It runs when the first message is sent, so the agent is at work
		// already. A conversation that is resumed has a count already.
		t := c.count(h)
		return Update{Status: Working, HasStatus: true, ContextTokens: t.context, InputTokens: t.in, OutputTokens: t.out}, true
	case "userPromptSubmitted":
		return Update{Status: Working, HasStatus: true}, true
	case "preToolUse":
		// A question to the user is a tool like the others.
		if h.ToolName == "ask_user" {
			return Update{Status: NeedsInput, HasStatus: true}, true
		}
		return Update{Status: Working, HasStatus: true}, true
	case "postToolUse":
		return Update{Status: Working, HasStatus: true, File: h.edited()}, true
	case "postToolUseFailure":
		return Update{Status: Working, HasStatus: true}, true
	case "notification":
		return notification(h.NotificationType)
	case "errorOccurred":
		// An error the agent recovers from is tried again: it is a sign of
		// life and nothing more. Any other ends the turn without an answer,
		// and what waits in the queue stays there.
		if h.Recoverable == nil || *h.Recoverable {
			return Update{}, true
		}
		return Update{Status: Idle, HasStatus: true, Failed: true}, true
	case "agentStop":
		t := c.count(h)
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

// edited returns the file a tool changed, if it is one that writes a file.
func (h copilotHook) edited() string {
	if h.ToolName != "edit" && h.ToolName != "create" {
		return ""
	}
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(h.ToolArgs, &args)
	return args.Path
}

// count returns the token count of the conversation a hook is from.
func (c *Copilot) count(h copilotHook) *tally {
	path := h.TranscriptPath
	if path == "" {
		path = copilotTranscript(h.SessionID)
	}
	return c.tallies.count(path, copilotLine)
}

// copilotLine counts one line of a Copilot CLI transcript, which records
// what each model reply cost. The input counts what was cached too.
func copilotLine(t *tally, line []byte) {
	var entry struct {
		Type string `json:"type"`
		Data struct {
			Usage struct {
				Input  int `json:"inputTokens"`
				Output int `json:"outputTokens"`
			} `json:"usage"`
		} `json:"data"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "session.usage_record" {
		return
	}
	u := entry.Data.Usage
	t.in, t.out, t.context = t.in+u.Input, t.out+u.Output, u.Input
}
