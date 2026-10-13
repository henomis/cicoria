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
	"os/exec"

	"github.com/henomis/cicoria/internal/ipc"
)

// Generic runs any command line as an agent. It has no knowledge of the
// program, so the session's status is guessed from terminal activity.
type Generic struct {
	name string
	argv []string
}

// NewGeneric returns an adapter for the given command line.
func NewGeneric(name string, argv []string) *Generic {
	return &Generic{name: name, argv: argv}
}

// Name implements Adapter.
func (g *Generic) Name() string { return g.name }

// Command implements Adapter.
func (g *Generic) Command(o LaunchOpts) *exec.Cmd { return baseCommand(o, g.argv) }

// ReportsStatus implements Adapter.
func (g *Generic) ReportsStatus() bool { return false }

// ReportsReady implements Adapter.
func (g *Generic) ReportsReady() bool { return false }

// ReportsAnswer implements Adapter.
func (g *Generic) ReportsAnswer() bool { return false }

// MapEvent implements Adapter.
func (g *Generic) MapEvent(ipc.Event) (Update, bool) { return Update{}, false }

// Mention implements Adapter.
func (g *Generic) Mention(path string) string { return path + " " }
