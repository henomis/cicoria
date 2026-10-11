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
