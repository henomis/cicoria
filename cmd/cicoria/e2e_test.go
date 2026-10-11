package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/henomis/cicoria/internal/term"
)

// fakeAgent stands in for Claude Code: it prints what it is told, and
// reports hook events through "cicoria hook" the way the real agent would.
const fakeAgent = `
hook() { printf '%s' "$1" | "$CICORIA_BIN" hook; }
echo "agent ready"
hook '{"hook_event_name":"SessionStart"}'
while read -r cmd arg; do
  case "$cmd" in
    work) hook '{"hook_event_name":"UserPromptSubmit"}' ;;
    ask)  hook '{"hook_event_name":"Notification","notification_type":"permission_prompt"}' ;;
    stop) hook '{"hook_event_name":"Stop"}' ;;
    edit) echo "hello from the agent" > "$arg"
          hook "{\"hook_event_name\":\"PostToolUse\",\"tool_input\":{\"file_path\":\"$PWD/$arg\"}}" ;;
    size) echo "size $(stty size)" ;;
    *)    echo "got $cmd" ;;
  esac
done
`

// workspace is a cicoria instance running in a PTY against a scratch project.
type workspace struct {
	t   *testing.T
	tm  *term.Terminal
	env []string
	bin string
	dir string
}

// buildBinary compiles cicoria into a temporary directory.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cicoria")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// newWorkspace prepares a git project and a config whose "claude" is the
// fake agent, with all state kept under temporary directories.
func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	bin := buildBinary(t)
	home := t.TempDir()
	dir := filepath.Join(home, "project")

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "api", "auth.go"), []byte("package api\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	config := fmt.Sprintf("notify = false\n\n[agents.claude]\ncommand = [\"sh\", \"-c\", %q, \"fake-claude\"]\n", fakeAgent)
	must(os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o644))
	must(os.MkdirAll(filepath.Join(home, "run"), 0o700))

	return &workspace{
		t:   t,
		bin: bin,
		dir: dir,
		env: append(os.Environ(),
			"CICORIA_BIN="+bin,
			"XDG_STATE_HOME="+filepath.Join(home, "state"),
			"XDG_RUNTIME_DIR="+filepath.Join(home, "run"),
			"CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"),
			"SHELL=/bin/sh",
		),
	}
}

// launch starts cicoria on the project.
func (w *workspace) launch(width, height int) {
	w.t.Helper()
	cmd := exec.Command(w.bin, "-config", filepath.Join(filepath.Dir(w.dir), "config.toml"), w.dir)
	cmd.Env = w.env
	tm, err := term.Start(cmd, width, height)
	if err != nil {
		w.t.Fatal(err)
	}
	w.tm = tm
	w.t.Cleanup(func() { _ = tm.Close() })
}

// see waits until the screen shows every one of the given texts.
func (w *workspace) see(wants ...string) {
	w.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		screen := w.tm.String()
		missing := ""
		for _, want := range wants {
			if !strings.Contains(screen, want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		select {
		case <-w.tm.Updates():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			w.t.Fatalf("screen never showed %q; got:\n%s", missing, screen)
		}
	}
}

// gone waits until the screen no longer shows text.
func (w *workspace) gone(text string) {
	w.t.Helper()
	deadline := time.After(10 * time.Second)
	for strings.Contains(w.tm.String(), text) {
		select {
		case <-w.tm.Updates():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			w.t.Fatalf("screen still shows %q:\n%s", text, w.tm.String())
		}
	}
}

// typeText types characters; line also presses enter.
func (w *workspace) typeText(text string) {
	for _, r := range text {
		w.tm.SendKey(uv.Key{Code: r, Text: string(r)})
	}
}

func (w *workspace) line(text string) {
	w.typeText(text)
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
}

// prefix presses the prefix key followed by a command key.
func (w *workspace) prefix(key rune) {
	w.tm.SendKey(uv.Key{Code: 'a', Mod: uv.ModCtrl})
	w.typeText(string(key))
}

// quit leaves cicoria and waits for it to end.
func (w *workspace) quit() {
	w.t.Helper()
	w.prefix('q')
	select {
	case <-w.tm.Done():
	case <-time.After(5 * time.Second):
		w.t.Fatalf("did not quit; screen:\n%s", w.tm.String())
	}
}

func TestWorkspace(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)

	// The agent runs in the centre pane, between the side panels.
	w.see("Sessions", "Files", "working tree clean", "agent ready", "claude-1 · claude · idle")
	w.see("▸ api/", "main.go")
	w.line("ping")
	w.see("got ping")
	// 140 columns less the panels (28 + 36) and the border; 40 rows less the
	// status bar and the border.
	w.line("size")
	w.see("size 37 74")

	// Hook events drive the status.
	w.line("work")
	w.see("claude-1 · claude · working")
	w.line("ask")
	w.see("claude-1 · claude · needs input")
	w.line("stop")
	w.see("claude-1 · claude · idle")

	// A file the agent writes shows up as a change.
	w.line("edit notes.txt")
	w.see("Changes (1)", "? notes.txt", "+1")
	w.line("stop")
	w.see("claude-1 · claude · idle")

	// Its diff opens on the whole screen and closes back to the workspace.
	w.prefix('g')
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("diff  notes.txt", "+hello from the agent")
	w.gone("Sessions")
	w.typeText("d")
	w.see("file  notes.txt", "1 hello from the agent")
	w.typeText("q")
	w.see("Sessions", "agent ready")

	// A file opens from the tree the same way.
	w.prefix('f')
	w.typeText("j") // api/, then main.go
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("file  main.go", "3 func main() {}")
	w.typeText("q")
	w.see("Sessions")

	// And by name, through the file finder.
	w.prefix('o')
	w.see("file ›")
	w.typeText("auth")
	w.see("api/auth.go")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("file  api/auth.go", "1 package api")
	w.typeText("q")
	w.see("Sessions")

	// A second session, then back to the first by number.
	w.prefix('c')
	w.see("agent ›")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("claude-2 · claude · idle")
	w.line("second")
	w.see("got second")
	w.prefix('1')
	w.see("claude-1 · claude · idle", "got ping")
	w.gone("got second")

	// A background session that blocks is announced in the status bar.
	w.prefix('2')
	w.line("work")
	w.see("claude-2 · claude · working")
	w.prefix('1')
	w.see("claude-1 · claude")
	w.prefix('2')
	w.line("ask")
	w.prefix('1')
	w.see("▲ claude-2 needs input")
	w.prefix('a')
	w.see("claude-2 · claude · needs input")
	w.line("stop")
	w.see("claude-2 · claude · idle")

	// Sessions are remembered across runs.
	w.quit()
	w.launch(140, 40)
	w.see("claude-1", "claude-2", "claude-2 · claude · idle")

	// Closing a session asks first.
	w.prefix('x')
	w.see("close claude-2 and stop its agent?")
	w.typeText("y")
	w.gone("claude-2")
	w.see("claude-1 · claude · idle")
	w.quit()
}

func TestNarrowTerminalHidesPanels(t *testing.T) {
	w := newWorkspace(t)
	w.launch(60, 20)
	w.see("agent ready")
	w.line("size")
	w.see("size 19 60")
	if strings.Contains(w.tm.String(), "Sessions") {
		t.Errorf("panels shown in a narrow terminal:\n%s", w.tm.String())
	}
	w.quit()
}

// TestRealAgent embeds the real Claude Code and checks that its hooks reach
// cicoria. It is a manual check, since it needs a logged-in Claude Code and a
// folder it already trusts: set CICORIA_E2E_REAL to that folder. No prompt is
// sent to the agent.
func TestRealAgent(t *testing.T) {
	dir := os.Getenv("CICORIA_E2E_REAL")
	if dir == "" {
		t.Skip("CICORIA_E2E_REAL not set")
	}
	w := &workspace{t: t, bin: buildBinary(t), dir: dir}

	// The stock configuration, so that "claude" is the real one.
	cmd := exec.Command(w.bin, "-config", filepath.Join(t.TempDir(), "none.toml"), dir)
	cmd.Env = append(os.Environ(), "XDG_STATE_HOME="+t.TempDir())
	tm, err := term.Start(cmd, 150, 40)
	if err != nil {
		t.Fatal(err)
	}
	w.tm = tm
	defer func() { _ = tm.Close() }()

	// The SessionStart hook is what moves the session out of "starting".
	w.see("claude-1 · claude · idle", "Claude Code")
	t.Logf("screen:\n%s", tm.String())
	w.quit()
}
