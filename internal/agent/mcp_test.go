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
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var wantMCPArgs = []string{"mcp", "-session", "abc", "-socket", "/run/s.sock"}

// mcpEntry reads cicoria's server out of an mcpServers JSON configuration.
func mcpEntry(t *testing.T, config string) map[string]any {
	t.Helper()
	var c struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(config), &c); err != nil {
		t.Fatalf("MCP config is not JSON: %v", err)
	}
	e := c.MCPServers["cicoria"]
	if e == nil {
		t.Fatalf("no cicoria server in %s", config)
	}
	if e["command"] != "/bin/cicoria" {
		t.Errorf("command = %v", e["command"])
	}
	var args []string
	for _, a := range e["args"].([]any) {
		args = append(args, a.(string))
	}
	if !slices.Equal(args, wantMCPArgs) {
		t.Errorf("args = %v, want %v", args, wantMCPArgs)
	}
	return e
}

func TestMCPServer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	o := LaunchOpts{SessionID: "abc", Dir: "/proj", Self: "/bin/cicoria", Socket: "/run/s.sock", Messages: true}

	t.Run("claude", func(t *testing.T) {
		args := NewClaude(nil).Command(o).Args
		i := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "--mcp-config=") })
		if i < 0 {
			t.Fatalf("no --mcp-config in %v", args)
		}
		mcpEntry(t, strings.TrimPrefix(args[i], "--mcp-config="))
		// The hook settings stay the last argument.
		if args[len(args)-2] != "--settings" {
			t.Errorf("args end on %v", args[len(args)-2:])
		}
	})

	t.Run("codex", func(t *testing.T) {
		args := NewCodex(nil).Command(o).Args
		i := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "mcp_servers.") })
		if i < 1 || args[i-1] != "-c" {
			t.Fatalf("no mcp_servers option in %v", args)
		}
		// Codex CLI reads the value as TOML.
		var c struct {
			MCPServers map[string]struct {
				Command string
				Args    []string
			} `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(args[i], &c); err != nil {
			t.Fatalf("%s is not TOML: %v", args[i], err)
		}
		if e := c.MCPServers["cicoria"]; e.Command != "/bin/cicoria" || !slices.Equal(e.Args, wantMCPArgs) {
			t.Errorf("server = %+v", e)
		}
	})

	t.Run("copilot", func(t *testing.T) {
		args := NewCopilot(nil).Command(o).Args
		i := slices.Index(args, "--additional-mcp-config")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("no --additional-mcp-config in %v", args)
		}
		if e := mcpEntry(t, args[i+1]); e["type"] != "local" {
			t.Errorf("type = %v", e["type"])
		}
	})

	// Turned off, or outside of cicoria, no agent gets the server.
	for _, o := range []LaunchOpts{
		{SessionID: "abc", Dir: "/proj", Self: "/bin/cicoria", Socket: "/run/s.sock"},
		{SessionID: "abc", Dir: "/proj", Messages: true},
	} {
		for _, a := range []Adapter{NewClaude(nil), NewCodex(nil), NewCopilot(nil)} {
			if args := strings.Join(a.Command(o).Args, " "); strings.Contains(args, "mcp") {
				t.Errorf("%s with %+v: %s", a.Name(), o, args)
			}
		}
	}
}
