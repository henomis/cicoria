package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/henomis/cicoria/internal/session"
)

// tree creates directories (names ending in "/") and files under a new root.
func tree(t *testing.T, names ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		if name[len(name)-1] == '/' {
			err = os.MkdirAll(path, 0o755)
		} else {
			err = os.WriteFile(path, nil, 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTilde(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	for in, want := range map[string]string{
		"/home/me":       "~",
		"/home/me/src/a": "~/src/a",
		"/home/meow/src": "/home/meow/src",
		"/srv/work":      "/srv/work",
	} {
		if got := tilde(in); got != want {
			t.Errorf("tilde(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDir(t *testing.T) {
	home := tree(t, "src/app/", "src/notes.txt")
	t.Setenv("HOME", home)
	base := filepath.Join(home, "src")

	for text, want := range map[string]string{
		"":                         base,
		"app":                      filepath.Join(base, "app"),
		"app/":                     filepath.Join(base, "app"),
		"..":                       home,
		"~/src/app":                filepath.Join(base, "app"),
		"~":                        home,
		filepath.Join(base, "app"): filepath.Join(base, "app"),
	} {
		got, err := resolveDir(base, text)
		if err != nil || got != want {
			t.Errorf("resolveDir(%q) = %q, %v; want %q", text, got, err, want)
		}
	}

	for _, text := range []string{"missing", "notes.txt", "~/nope"} {
		if got, err := resolveDir(base, text); err == nil {
			t.Errorf("resolveDir(%q) = %q, want an error", text, got)
		}
	}
}

func TestCompleteDir(t *testing.T) {
	home := tree(t, "src/alpha/", "src/alpine/", "src/beta/", "src/.hidden/", "src/almanac.txt", "src/beta/deep/")
	t.Setenv("HOME", home)
	base := filepath.Join(home, "src")

	for text, want := range map[string]string{
		"b":           "beta/",       // the only match, ready for the next part
		"al":          "alp",         // what alpha and alpine share; the file does not count
		"alp":         "alp",         // nothing more to add
		"x":           "x",           // no match
		"beta/":       "beta/deep/",  // inside a directory
		"~/src/be":    "~/src/beta/", // keeps the form it was typed in
		"~":           "~/",
		".h":          ".hidden/",      // hidden only when asked for
		base + "/bet": base + "/beta/", // absolute
	} {
		if got := completeDir(base, text); got != want {
			t.Errorf("completeDir(%q) = %q, want %q", text, got, want)
		}
	}

	// With nothing typed, hidden directories are not among the candidates.
	if got := completeDir(base, ""); got != "" {
		t.Errorf("completeDir(\"\") = %q, want nothing in common", got)
	}
}

func TestLasting(t *testing.T) {
	for want, ago := range map[string]time.Duration{
		"":    30 * time.Second,
		"1m":  90 * time.Second,
		"59m": 59*time.Minute + 30*time.Second,
		"2h":  2*time.Hour + time.Minute,
		"4d":  100 * time.Hour,
	} {
		s := &session.Session{Since: time.Now().Add(-ago)}
		if got := lasting(s); got != want {
			t.Errorf("lasting after %v = %q, want %q", ago, got, want)
		}
	}
	if got := lasting(&session.Session{}); got != "" {
		t.Errorf("lasting of a session that never started = %q", got)
	}
}
