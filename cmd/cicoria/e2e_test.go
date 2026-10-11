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
    fail) hook '{"hook_event_name":"StopFailure"}' ;;
    news) hook '{"hook_event_name":"Notification","notification_type":"auth_success"}' ;;
    edit) echo "hello from the agent" > "$arg"
          hook "{\"hook_event_name\":\"PostToolUse\",\"tool_input\":{\"file_path\":\"$PWD/$arg\"}}" ;;
    size) echo "size $(stty size)" ;;
    pwd)  echo "in $PWD" ;;
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
	// A second project, which is not a git repository.
	must(os.MkdirAll(filepath.Join(home, "other"), 0o755))
	must(os.WriteFile(filepath.Join(home, "other", "elsewhere.txt"), []byte("hi\n"), 0o644))
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

	config := fmt.Sprintf("notify = false\nanimations = false\n\n[agents.claude]\ncommand = [\"sh\", \"-c\", %q, \"fake-claude\"]\n", fakeAgent)
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

// set adds a line to the settings cicoria is launched with.
func (w *workspace) set(line string) {
	w.t.Helper()
	path := filepath.Join(filepath.Dir(w.dir), "config.toml")
	old, err := os.ReadFile(path)
	if err == nil {
		// Before the tables at the end of the file.
		err = os.WriteFile(path, append([]byte(line+"\n"), old...), 0o644)
	}
	if err != nil {
		w.t.Fatal(err)
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
	w.see("Sessions", "Files · project", "working tree clean", "agent ready", "claude-1 · claude · idle")
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

	// News that asks for nothing is not a request, and a turn that fails is
	// over.
	w.line("work")
	w.see("claude-1 · claude · working")
	w.line("news")
	w.line("pong")
	w.see("got pong", "claude-1 · claude · working")
	w.line("fail")
	w.see("claude-1 · claude · idle", "claude-1: the turn ended on an error")

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
	w.see("claude in directory: ")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("session name: claude-2")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("claude-2 · claude · idle")
	// Two sessions in one directory cannot be told apart by their changes.
	w.see("claude-1 already works in this directory")
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

	// A session can work in another project: the directory is asked for,
	// with completion, and the side panels follow the session on screen.
	w.prefix('c')
	w.see("agent ›")
	w.typeText("shell")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("shell in directory: ")
	w.tm.SendKey(uv.Key{Code: 'u', Mod: uv.ModCtrl})
	w.typeText("../oth")
	w.tm.SendKey(uv.Key{Code: uv.KeyTab})
	w.see("shell in directory: ../other/")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("session name: shell-1")
	w.tm.SendKey(uv.Key{Code: 'u', Mod: uv.ModCtrl})
	w.typeText("scratch")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("scratch · shell", "Files · other", "elsewhere.txt", "not a git repository")
	w.gone("main.go")
	w.line("pwd")
	w.see(filepath.Join(filepath.Dir(w.dir), "other"))
	w.prefix('1')
	w.see("Files · project", "main.go", "Changes (1)")
	w.gone("elsewhere.txt")

	// A directory that does not exist is refused.
	w.prefix('c')
	w.see("agent ›")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("claude in directory: ")
	w.typeText("/nope")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("does not exist")

	// The directory of each session is remembered too.
	w.quit()
	w.launch(140, 40)
	w.see("claude-1", "claude-2", "scratch")
	w.prefix('3')
	w.see("scratch · shell", "Files · other", "elsewhere.txt")
	w.prefix('2')
	w.see("claude-2 · claude · idle", "Files · project")

	// Closing a session asks first.
	w.prefix('x')
	w.see("close claude-2 and stop its agent?")
	w.typeText("y")
	w.gone("claude-2")
	// The session that takes its place comes on screen, with its project.
	w.see("scratch · shell", "Files · other")
	w.quit()
}

// Sessions belong to the directory cicoria was opened on. Opened on another
// one, it says where they are and goes there when asked.
func TestOtherWorkspaces(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)
	w.see("agent ready", "claude-1 · claude · idle")
	w.prefix('w')
	w.see("no other workspace is saved")

	// A session of this workspace that works in another directory.
	w.prefix('c')
	w.see("agent ›")
	w.typeText("shell")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("shell in directory: ")
	w.tm.SendKey(uv.Key{Code: 'u', Mod: uv.ModCtrl})
	w.typeText("../other")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("session name: shell-1")
	w.tm.SendKey(uv.Key{Code: 'u', Mod: uv.ModCtrl})
	w.typeText("scratch")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("scratch · shell", "Files · other")
	w.quit()

	// Opened on that directory, cicoria has no session of its own to show,
	// and points at the one that works there.
	w.dir = filepath.Join(filepath.Dir(w.dir), "other")
	w.launch(140, 40)
	w.see("claude-1 · claude · idle", "scratch works here from the workspace")
	w.prefix('w')
	w.see("workspace ›", "project", "scratch works here")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("scratch · shell", "claude-1", "Files · other")
	w.prefix('1')
	w.see("claude-1 · claude · idle", "Files · project", "main.go")

	// The way back is there too.
	w.prefix('w')
	w.see("workspace ›", "other", "1 session(s)")
	w.tm.SendKey(uv.Key{Code: uv.KeyEscape})
	w.gone("workspace ›")
	w.quit()
}

// Unless the settings say otherwise, the changes are what is not committed,
// and what a session did since it started is one key away.
func TestChangesAreUncommittedByDefault(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)
	w.see("agent ready", "working tree clean")

	w.line("edit notes.txt")
	w.see("Changes (1)", "? notes.txt")
	w.line("stop")
	w.see("claude-1 · claude · idle")
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "notes"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", w.dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	w.see("working tree clean")
	w.gone("? notes.txt")

	w.prefix('g')
	w.typeText("s")
	w.see("everything since it started", "Changes (1) · 1 commit", "A notes.txt")
	w.quit()
}

// ignore makes git ignore a file and a folder of the project.
func (w *workspace) ignore() {
	w.t.Helper()
	for name, content := range map[string]string{
		".gitignore":    "secret.env\nbuild/\n",
		"secret.env":    "KEY=1\n",
		"build/out.bin": "x\n",
	} {
		path := filepath.Join(w.dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
}

// What git ignores is in the files panel too, an ignored folder with what
// it holds once it is open, and one key hides it all.
func TestIgnoredFiles(t *testing.T) {
	w := newWorkspace(t)
	w.ignore()
	w.launch(140, 40)
	w.see("agent ready", "main.go", "secret.env", "▸ build/")

	// Folders come first: api, then build.
	w.prefix('f')
	w.typeText("jl")
	w.see("▾ build/", "out.bin")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.gone("out.bin")

	w.typeText("i")
	w.see("ignored files hidden")
	w.gone("secret.env")
	w.gone("build/")
	w.typeText("i")
	w.see("ignored files shown", "secret.env", "▸ build/")
	w.quit()
}

// The settings can leave what git ignores out until it is asked for.
func TestIgnoredFilesHiddenBySetting(t *testing.T) {
	w := newWorkspace(t)
	w.ignore()
	w.set(`ignored_files = false`)
	w.launch(140, 40)
	w.see("agent ready", "main.go", ".gitignore")
	w.gone("secret.env")

	w.prefix('f')
	w.typeText("i")
	w.see("ignored files shown", "secret.env", "▸ build/")
	w.quit()
}

// The changes of a session can be counted from where it started, so that
// work the agent commits is still there to review.
func TestReviewSinceTheStart(t *testing.T) {
	w := newWorkspace(t)
	w.set(`changes = "session"`)
	w.launch(140, 40)
	w.see("agent ready", "working tree clean")

	w.line("edit notes.txt")
	w.see("Changes (1)", "? notes.txt")
	w.line("stop")
	w.see("claude-1 · claude · idle")
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "notes"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", w.dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	w.see("Changes (1) · 1 commit", "A notes.txt")

	// Its diff is against the start, not against the commit.
	w.prefix('g')
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("diff  notes.txt", "+hello from the agent")
	w.typeText("q")
	w.see("Sessions")

	// What is not committed is one key away, and back.
	w.typeText("s")
	w.see("what is not committed", "working tree clean")
	w.gone("A notes.txt")
	w.typeText("s")
	w.see("everything since it started", "Changes (1) · 1 commit")

	// Once it has been looked at, the count starts again, and stays so.
	w.prefix('h')
	w.see("counted from here", "working tree clean")
	w.gone("1 commit")
	w.quit()
	w.launch(140, 40)
	w.see("agent ready", "working tree clean")
	w.gone("1 commit")
	w.quit()
}

// Notes are written on lines of a diff or a file and reach the agent as one
// message, at once or through the queue.
func TestNotes(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)
	w.see("agent ready")
	w.line("edit notes.txt")
	w.see("Changes (1)")
	w.line("stop")
	w.see("claude-1 · claude · idle")

	// In a diff, the lines of the file can carry a note; its headers cannot.
	w.prefix('g')
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("diff  notes.txt", "+hello from the agent")
	w.typeText("c")
	w.see("no line of the file here")
	w.typeText("Gc")
	w.see("note on notes.txt:1:")
	w.line("say hi properly")
	w.see("1 note(s)", "*▌+hello from the agent")
	w.typeText("q")
	w.see("Sessions")

	// In a file, several lines are selected first.
	w.prefix('o')
	w.see("file ›")
	w.typeText("main")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("file  main.go")
	w.typeText("vjjc")
	w.see("note on main.go:1-3:")
	w.line("add a doc comment")
	w.see("2 note(s)", "* 1 package main", "*▌3 func main() {}")

	// Leaving the question of how to send them goes back to the file.
	w.typeText("s")
	w.see("send 2 note(s) to claude-1", "Send to the agent now", "Add to the queue")
	w.tm.SendKey(uv.Key{Code: uv.KeyEscape})
	w.see("file  main.go", "2 note(s)")

	// Sent at once, the agent gets one message with every note.
	w.typeText("s")
	w.see("Send to the agent now")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("got Review", "got notes.txt:1", "got say", "got main.go:1-3", "got add")
	w.gone("note(s)")

	// Through the queue, they wait for the end of the turn. A note can be
	// rewritten and deleted before it goes.
	w.prefix('o')
	w.see("file ›")
	w.typeText("main")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("file  main.go")
	w.typeText("jjc")
	w.see("note on main.go:3:")
	w.line("too short")
	w.see("1 note(s)")
	w.typeText("x")
	w.gone("note(s)")
	w.typeText("c")
	w.line("too short")
	w.typeText("c")
	w.see("note on main.go:3: too short")
	w.line("ly named")
	w.see("1 note(s)")
	w.typeText("s")
	w.see("Add to the queue")
	w.tm.SendKey(uv.Key{Code: uv.KeyDown})
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("Queue (1)", "▸ Review notes: …")
	w.line("stop")
	w.see("got main.go:3", "too shortly named", "got too")
	w.gone("Queue (")
	w.line("stop")
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

func TestHidePanelsBySide(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)
	w.see("agent ready", "Sessions", "Changes")

	w.prefix('[')
	w.gone("Sessions")
	w.see("Changes")
	// The agent gets the room of the left panels: 140 less 36 and the border.
	w.line("size")
	w.see("size 37 102")

	w.prefix(']')
	w.gone("Changes")
	// Asking for a panel brings its side back, and only that one.
	w.prefix('g')
	w.see("Changes")
	if strings.Contains(w.tm.String(), "Sessions") {
		t.Errorf("left panels came back with the right ones:\n%s", w.tm.String())
	}
	// With one side on screen, b hides it; again, and both are back.
	w.prefix('b')
	w.gone("Changes")
	w.prefix('b')
	w.see("Sessions", "Changes")
	w.quit()
}

func TestAnimations(t *testing.T) {
	w := newWorkspace(t)
	path := filepath.Join(filepath.Dir(w.dir), "config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), "animations = false", "animations = true", 1))
	if err := os.WriteFile(path, config, 0o644); err != nil {
		t.Fatal(err)
	}

	// The chicory grows over the name while the agent starts under it.
	w.launch(140, 40)
	w.see("─ cicoria ", "c i c o r i a", "( @ )")
	w.see("agent ready")

	// A new session lifts off, and a key brings the agent forward.
	w.prefix('c')
	w.see("agent ›")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("in directory: ")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("session name: ")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("launching claude-2", "|[]|")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.gone("launching claude-2")
	w.see("claude-2 · claude", "agent ready")

	// A closed session goes in a puff.
	w.prefix('x')
	w.see("close claude-2")
	w.typeText("y")
	w.see("claude-2 closed", "*poof*")
	w.see("claude-1 · claude")

	w.prefix('q')
	w.see("─ ciao ", "c i a o !")
	select {
	case <-w.tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", w.tm.String())
	}
}

// A side that does not fit next to the one on screen stays hidden, and
// leaves the other where it is.
func TestNarrowTerminalKeepsTheSideOnScreen(t *testing.T) {
	w := newWorkspace(t)
	path := filepath.Join(filepath.Dir(w.dir), "config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("left_panel = false\n"), config...), 0o644); err != nil {
		t.Fatal(err)
	}

	// 70 columns have room for the agent and one side, not both.
	w.launch(70, 20)
	w.see("agent ready", "Changes")
	w.prefix('s')
	w.see("too narrow", "Changes")
	if strings.Contains(w.tm.String(), "Sessions") {
		t.Errorf("the left panels are on screen:\n%s", w.tm.String())
	}
	// The agent still has the keys, and its size.
	w.line("size")
	w.see("size 17 40")
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

// queue adds messages to the queue of the session on screen.
func (w *workspace) queue(messages ...string) {
	w.t.Helper()
	for _, msg := range messages {
		w.prefix('m')
		w.see("queue for ")
		w.typeText(msg)
		w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
		// One message is asked for at a time.
		w.gone("queue for ")
	}
}

func TestQueue(t *testing.T) {
	w := newWorkspace(t)
	w.launch(140, 40)
	w.see("claude-1 · claude · idle")

	// To an idle agent the message goes at once. It counts as working from
	// then on, until it says that its turn is over.
	w.queue("one")
	w.see("got one", "claude-1 · claude · working")
	w.gone("Queue (")

	// While the agent works, messages wait; each end of turn sends one.
	w.queue("two", "three")
	w.see("Queue (2)", "▸ two", "three", "⋯2", "queue 2")
	if strings.Contains(w.tm.String(), "got two") {
		t.Fatal("a message was sent while the agent was working")
	}
	w.line("stop")
	w.see("got two", "Queue (1)", "▸ three")
	if strings.Contains(w.tm.String(), "got three") {
		t.Fatal("two messages were sent for one end of turn")
	}
	w.line("stop")
	w.see("got three")
	w.gone("Queue (")

	// Nothing is sent to an agent that waits for a permission.
	w.queue("four")
	w.line("ask")
	w.see("claude-1 · claude · needs input", "Queue (1)")
	if strings.Contains(w.tm.String(), "got four") {
		t.Fatal("a message was sent to an agent that needs input")
	}
	w.line("stop")
	w.see("got four")

	// A paused queue holds its messages through the end of a turn, and
	// sends when it is released.
	w.queue("five")
	w.prefix('u')
	w.typeText("p")
	w.see("Queue (1) · paused", "queue 1 paused")
	w.typeText("q") // leaves the panel
	w.line("stop")
	w.see("claude-1 · claude · idle")
	if strings.Contains(w.tm.String(), "got five") {
		t.Fatal("a paused queue sent a message")
	}
	w.prefix('u')
	w.typeText("p")
	w.see("got five")
	w.gone("Queue (")
	w.line("stop")
	w.see("claude-1 · claude · idle")

	// Messages can be reordered and deleted before they go.
	w.line("work")
	w.see("claude-1 · claude · working")
	w.queue("six", "seven", "eight")
	w.see("Queue (3)", "▸ six")
	w.prefix('u')
	w.typeText("K") // already first: stays
	w.typeText("J") // six goes below seven
	w.see("▸ seven")
	w.typeText("x") // deletes six, which the cursor followed
	w.see("Queue (2)", "▸ seven", "eight")
	w.gone("six")
	w.typeText("q")
	w.line("stop")
	w.see("got seven")
	w.line("stop")
	w.see("got eight")
	w.line("stop")
	w.see("claude-1 · claude · idle")

	// An agent that does not report its status gets a message only when
	// asked to. The message prints 42x when the shell runs it.
	w.prefix('c')
	w.see("agent ›")
	w.typeText("shell")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("shell in directory: ")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("session name: ")
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("shell-1 · shell")
	w.queue("echo $((6*7))x")
	w.see("Queue (1) · manual")
	time.Sleep(2 * time.Second) // long enough for the shell to look idle
	if strings.Contains(w.tm.String(), "42x") {
		t.Fatal("a message was sent unasked to an agent that does not report its status")
	}
	w.prefix('u')
	w.tm.SendKey(uv.Key{Code: uv.KeyEnter})
	w.see("42x")
	w.gone("Queue (")

	// A queue left behind is restored, held back.
	w.prefix('1')
	w.line("work")
	w.see("claude-1 · claude · working")
	w.queue("later")
	w.see("Queue (1)")
	w.prefix('q')
	w.see("still working")
	w.typeText("y")
	select {
	case <-w.tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("did not quit; screen:\n%s", w.tm.String())
	}
	w.launch(140, 40)
	w.see("claude-1 · claude · idle", "Queue (1) · paused", "▸ later")
	if strings.Contains(w.tm.String(), "got later") {
		t.Fatal("a restored queue sent a message by itself")
	}
	w.prefix('u')
	w.typeText("p")
	w.see("got later")
}
