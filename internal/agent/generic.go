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

// MapEvent implements Adapter.
func (g *Generic) MapEvent(ipc.Event) (Update, bool) { return Update{}, false }

// Mention implements Adapter.
func (g *Generic) Mention(path string) string { return path + " " }
