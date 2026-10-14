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

package term

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// waitFor polls the screen until it contains want.
func waitFor(t *testing.T, tm *Terminal, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(tm.String(), want) {
			return
		}
		select {
		case <-tm.Updates():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("screen never showed %q; got:\n%s", want, tm.String())
		}
	}
}

func TestOutputReachesScreen(t *testing.T) {
	tm, err := Start(exec.Command("printf", `hello \033[31mred\033[0m`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()

	waitFor(t, tm, "hello red")
	if !strings.Contains(tm.Render(), "\x1b[") {
		t.Errorf("Render lost the styling: %q", tm.Render())
	}

	select {
	case <-tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("command did not exit")
	}
	if tm.Err() != nil {
		t.Errorf("exit error: %v", tm.Err())
	}
}

func TestKeysReachCommand(t *testing.T) {
	tm, err := Start(exec.Command("cat"), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()

	for _, k := range []uv.Key{
		{Code: 'h', Text: "h"},
		{Code: 'i', Text: "I", Mod: uv.ModShift},
		{Code: uv.KeyEnter},
	} {
		tm.SendKey(k)
	}
	// The tty echoes the line and cat prints it back.
	waitFor(t, tm, "hI\nhI")

	tm.SendKey(uv.Key{Code: 'd', Mod: uv.ModCtrl})
	select {
	case <-tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctrl+d did not end cat")
	}
}

func TestScrollBack(t *testing.T) {
	tm, err := Start(exec.Command("sh", "-c", `seq 1 50; echo end; cat`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()
	waitFor(t, tm, "end")

	// The screen holds 48, 49, 50, end and the line of the cursor.
	tm.Scroll(3)
	if got := tm.Scrolled(); got != 3 {
		t.Fatalf("Scrolled = %d, want 3", got)
	}
	if r := tm.Render(); !strings.Contains(r, "45") || !strings.Contains(r, "49") || strings.Contains(r, "50") || strings.Contains(r, "end") {
		t.Errorf("three lines back shows %q", r)
	}
	if _, _, visible := tm.Cursor(); visible {
		t.Error("the cursor shows while scrolled back")
	}

	// There is no going past either end.
	tm.Scroll(1000)
	if r := tm.Render(); !strings.Contains(r, "1") || strings.Contains(r, "6") {
		t.Errorf("the start shows %q", r)
	}
	tm.Scroll(-2000)
	if got := tm.Scrolled(); got != 0 {
		t.Errorf("Scrolled = %d at the end", got)
	}

	// A key goes to the end.
	tm.Scroll(3)
	tm.SendKey(uv.Key{Code: 'x', Text: "x"})
	if r := tm.Render(); tm.Scrolled() != 0 || !strings.Contains(r, "end") {
		t.Errorf("after a key, %d back: %q", tm.Scrolled(), r)
	}
}

// A zsh prompt hides the cursor, saves and restores it, and shows it again.
func TestCursorShowsAfterRestore(t *testing.T) {
	tm, err := Start(exec.Command("sh", "-c", `printf '\0337\033[?25lhidden'; sleep 0.2; printf '\0338\033[?25hshown'; cat`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()
	waitFor(t, tm, "hidden")
	if _, _, visible := tm.Cursor(); visible {
		t.Error("the cursor shows while the command hides it")
	}
	waitFor(t, tm, "shown")
	if _, _, visible := tm.Cursor(); !visible {
		t.Error("the cursor stays hidden once the command shows it again")
	}
}

func TestCommandThatReadsTheMouseOwnsScrolling(t *testing.T) {
	tm, err := Start(exec.Command("sh", "-c", `echo plain; read x; printf '\033[?1000hmouse\n'; read x; printf '\033[?1000lnone\n'; cat`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()

	waitFor(t, tm, "plain")
	if tm.OwnsScrolling() {
		t.Error("a plain command owns scrolling")
	}
	tm.SendKey(uv.Key{Code: uv.KeyEnter})
	waitFor(t, tm, "mouse")
	if !tm.OwnsScrolling() {
		t.Error("a command that reads the mouse does not own scrolling")
	}
	tm.SendKey(uv.Key{Code: uv.KeyEnter})
	waitFor(t, tm, "none")
	if tm.OwnsScrolling() {
		t.Error("a command that stopped reading the mouse still owns scrolling")
	}
}

func TestResizeReachesCommand(t *testing.T) {
	tm, err := Start(exec.Command("sh", "-c", `trap 'stty size' WINCH; echo ready; while :; do sleep 0.05; done`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()

	waitFor(t, tm, "ready")
	tm.Resize(100, 30)
	waitFor(t, tm, "30 100")
}

func TestEncodeKey(t *testing.T) {
	tests := []struct {
		name string
		key  uv.Key
		want string
		ok   bool
	}{
		{"plain text", uv.Key{Code: 'a', Text: "a"}, "a", true},
		{"shifted text", uv.Key{Code: 'a', Text: "A", Mod: uv.ModShift}, "A", true},
		{"non-ascii text", uv.Key{Code: 'è', Text: "è"}, "è", true},
		{"alt text", uv.Key{Code: 'b', Text: "b", Mod: uv.ModAlt}, "\x1bb", true},
		{"shift+enter", uv.Key{Code: uv.KeyEnter, Mod: uv.ModShift}, "\x1b\r", true},
		{"alt+enter", uv.Key{Code: uv.KeyEnter, Mod: uv.ModAlt}, "\x1b\r", true},
		{"enter", uv.Key{Code: uv.KeyEnter}, "", false},
		{"ctrl+c", uv.Key{Code: 'c', Mod: uv.ModCtrl}, "", false},
		{"arrow", uv.Key{Code: uv.KeyUp}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := encodeKey(tt.key)
			if got != tt.want || ok != tt.ok {
				t.Errorf("encodeKey(%+v) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// A title holding a character with the byte 0x9C in its UTF-8 form must not
// spill onto the screen, however the output is cut into reads.
func TestTitleStaysOffScreen(t *testing.T) {
	tm, err := Start(exec.Command("printf", `> \033]0;✳ Side panel\007\033]2;✳ two\033\\✳ done`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.Close() }()

	waitFor(t, tm, "done")
	if got := strings.TrimSpace(tm.String()); got != "> ✳ done" {
		t.Errorf("screen = %q", got)
	}

	out := []byte("a\x1b]0;\xe2\x9c\xb3 t\x07\xe2\x9c\xb3\x1bP\x9cq\x1b\\\x9c")
	want := "a\x1b]0;\xe2?\xb3 t\x07\xe2\x9c\xb3\x1bP?q\x1b\\\x9c"
	var f stringFilter
	for i := range out {
		f.filter(out[i : i+1])
	}
	if string(out) != want {
		t.Errorf("filtered = %q, want %q", out, want)
	}
}

// A command is told that its terminal is gone, and ends by itself.
func TestCloseLetsCommandEnd(t *testing.T) {
	left := filepath.Join(t.TempDir(), "left")
	tm, err := Start(exec.Command("sh", "-c", `trap 'echo bye > "$0"; exit' HUP; echo ready; while :; do sleep 0.05; done`, left), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tm, "ready")

	began := time.Now()
	_ = tm.Close()
	if took := time.Since(began); took >= grace {
		t.Errorf("Close took %v: the command was not left to end by itself", took)
	}
	if _, err := os.Stat(left); err != nil {
		t.Errorf("the command was stopped before it could end: %v", err)
	}
}

// A command that does not end when told to is killed, with what it started.
func TestCloseKillsCommandThatStays(t *testing.T) {
	tm, err := Start(exec.Command("sh", "-c", `trap '' HUP; sleep 300 & echo "child $!"; echo ready; wait`), 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tm, "ready")
	var child int
	for _, line := range strings.Split(tm.String(), "\n") {
		_, _ = fmt.Sscanf(line, "child %d", &child)
	}
	if child == 0 {
		t.Fatalf("no child on the screen:\n%s", tm.String())
	}

	_ = tm.Close()
	select {
	case <-tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the command is still there")
	}
	// A process that is gone cannot be signalled, once it has been reaped.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("what the command started is still there")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
