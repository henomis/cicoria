// Package term runs a command in a pseudo-terminal and keeps an emulated
// screen of its output, so the command can be drawn inside a pane.
package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Terminal is a running command attached to a PTY and a virtual screen.
type Terminal struct {
	cmd *exec.Cmd
	pty *os.File

	// mu guards emu. Reading the emulator's input pipe needs no lock, and must
	// not take one: the emulator writes to that pipe while mu is held.
	mu  sync.Mutex
	emu *vt.Emulator
	// back is how many lines of history are shown above the screen, and
	// mouse the mouse modes the command has asked for. Both are guarded by mu.
	back  int
	mouse map[ansi.Mode]bool

	updates chan struct{}
	done    chan struct{}
	exitErr error

	cursorHidden atomic.Bool
	bell         atomic.Bool
	closeOnce    sync.Once
}

// Start launches cmd in a PTY of the given size.
func Start(cmd *exec.Cmd, width, height int) (*Terminal, error) {
	width, height = max(width, 1), max(height, 1)

	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color", "COLORTERM=truecolor")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}

	t := &Terminal{
		cmd:     cmd,
		pty:     f,
		emu:     vt.NewEmulator(width, height),
		mouse:   map[ansi.Mode]bool{},
		updates: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	t.emu.SetCallbacks(vt.Callbacks{
		Bell:             func() { t.bell.Store(true) },
		CursorVisibility: func(visible bool) { t.cursorHidden.Store(!visible) },
		// The emulator calls these while it is written to, with mu held.
		EnableMode:  func(mode ansi.Mode) { t.setMouse(mode, true) },
		DisableMode: func(mode ansi.Mode) { t.setMouse(mode, false) },
	})

	go t.readOutput()
	go t.writeInput()

	return t, nil
}

// readOutput feeds the command's output to the emulator until the PTY closes.
func (t *Terminal) readOutput() {
	buf := make([]byte, 32*1024)
	var control stringFilter
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			control.filter(buf[:n])
			t.mu.Lock()
			before := t.emu.ScrollbackLen()
			_, _ = t.emu.Write(buf[:n])
			// What is being read stays where it is while more is written.
			if t.back > 0 {
				t.back = min(t.back+max(t.emu.ScrollbackLen()-before, 0), t.emu.ScrollbackLen())
			}
			t.mu.Unlock()
			t.notify()
		}
		if err != nil {
			break
		}
	}

	t.exitErr = t.cmd.Wait()
	close(t.done)
	t.notify()
}

// writeInput forwards what the emulator produces (encoded keys, mouse events
// and replies to terminal queries) to the command.
func (t *Terminal) writeInput() {
	_, _ = io.Copy(t.pty, t.emu)
	// Keep draining once the command is gone, so senders never block.
	_, _ = io.Copy(io.Discard, t.emu)
}

func (t *Terminal) notify() {
	select {
	case t.updates <- struct{}{}:
	default:
	}
}

// Updates signals that the screen changed or the command exited. Signals are
// coalesced: one receive may stand for many changes.
func (t *Terminal) Updates() <-chan struct{} { return t.updates }

// Done is closed when the command has exited.
func (t *Terminal) Done() <-chan struct{} { return t.done }

// Err returns the command's exit error. It is only valid after Done is closed.
func (t *Terminal) Err() error { return t.exitErr }

// Render returns the screen as text styled with ANSI escape codes. Scrolled
// back, the lines that left the screen take the place of its last ones.
func (t *Terminal) Render() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	back := t.scrolled()
	if back == 0 {
		return t.emu.Render()
	}

	history, height := t.emu.Scrollback(), t.emu.Height()
	lines := make([]string, 0, height)
	for i := history.Len() - back; i < history.Len() && len(lines) < height; i++ {
		lines = append(lines, history.Line(i).Render())
	}
	screen := strings.Split(t.emu.Render(), "\n")
	lines = append(lines, screen[:min(height-len(lines), len(screen))]...)
	return strings.Join(lines, "\n")
}

// setMouse records that the command turned a mouse mode on or off.
func (t *Terminal) setMouse(mode ansi.Mode, on bool) {
	switch mode {
	case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
		if on {
			t.mouse[mode] = true
		} else {
			delete(t.mouse, mode)
		}
	}
}

// scrolled returns how far back the screen is scrolled. A command on the
// alternate screen has no history. The caller holds mu.
func (t *Terminal) scrolled() int {
	if t.emu.IsAltScreen() {
		return 0
	}
	return min(t.back, t.emu.ScrollbackLen())
}

// OwnsScrolling reports whether scrolling is left to the command: it reads
// the mouse itself, or draws on the alternate screen as an editor does.
// Otherwise the terminal scrolls through what has left the screen.
func (t *Terminal) OwnsScrolling() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.mouse) > 0 || t.emu.IsAltScreen()
}

// Scroll moves the screen back through its history by lines, or forward
// when lines is negative.
func (t *Terminal) Scroll(lines int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.back = max(min(t.scrolled()+lines, t.emu.ScrollbackLen()), 0)
}

// Scrolled returns how many lines back the screen is scrolled.
func (t *Terminal) Scrolled() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrolled()
}

// String returns the screen as plain text.
func (t *Terminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.String()
}

// Cursor returns the cursor position and whether it should be shown. It is
// not shown while the screen is scrolled back.
func (t *Terminal) Cursor() (x, y int, visible bool) {
	t.mu.Lock()
	p, back := t.emu.CursorPosition(), t.scrolled()
	t.mu.Unlock()
	return p.X, p.Y, back == 0 && !t.cursorHidden.Load()
}

// TakeBell reports whether the command rang the bell since the last call.
func (t *Terminal) TakeBell() bool { return t.bell.Swap(false) }

// Resize changes the size of the screen and tells the command about it.
func (t *Terminal) Resize(width, height int) {
	width, height = max(width, 1), max(height, 1)

	t.mu.Lock()
	defer t.mu.Unlock()
	if width == t.emu.Width() && height == t.emu.Height() {
		return
	}
	t.emu.Resize(width, height)
	_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
}

// SendKey sends a key press to the command. What is typed is typed at the
// end, so the screen leaves its history.
func (t *Terminal) SendKey(k uv.Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.back = 0
	if seq, ok := encodeKey(k); ok {
		t.emu.SendText(seq)
		return
	}
	// The emulator matches keys by exact value, so drop what it ignores.
	k.Text = ""
	t.emu.SendKey(uv.KeyPressEvent(k))
}

// SendMouse sends a mouse event to the command, if it asked for them.
func (t *Terminal) SendMouse(m uv.MouseEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emu.SendMouse(m)
}

// Type sends text to the command as if it had been typed.
func (t *Terminal) Type(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emu.SendText(text)
}

// Paste sends pasted text to the command.
func (t *Terminal) Paste(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.back = 0
	t.emu.Paste(text)
}

// Close terminates the command and releases the PTY.
func (t *Terminal) Close() error {
	var err error
	t.closeOnce.Do(func() {
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		err = t.pty.Close()
		// Closing the input pipe ends writeInput. Emulator.Close would do the
		// same, but it also sets a flag that Read checks without a lock.
		if c, ok := t.emu.InputPipe().(io.Closer); ok {
			err = errors.Join(err, c.Close())
		}
	})
	return err
}
