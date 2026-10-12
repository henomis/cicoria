package term

import (
	"os/exec"
	"strings"
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
