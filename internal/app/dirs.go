package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tilde shortens a path under the user's home directory to start with "~".
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// expand turns what the user typed into an absolute path: "~" is the home
// directory and a relative path starts from base.
func expand(base, text string) string {
	if text == "~" || strings.HasPrefix(text, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			text = home + text[1:]
		}
	}
	if !filepath.IsAbs(text) {
		text = filepath.Join(base, text)
	}
	return text
}

// resolveDir returns the existing directory the user named. Nothing typed
// means base itself.
func resolveDir(base, text string) (string, error) {
	if text == "" {
		return base, nil
	}
	dir := filepath.Clean(expand(base, text))
	// Agents see the real path, so use the same one to match what they report.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", tilde(dir))
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", tilde(dir))
	}
	return real, nil
}

// completeDir extends a partly typed directory as far as the directories
// that exist allow: to the only match, or to what all matches share. The
// text keeps the form it was typed in.
func completeDir(base, text string) string {
	// Split what was typed into the directory to look in and the start of
	// the name looked for.
	cut := strings.LastIndex(text, "/") + 1
	typedDir, partial := text[:cut], text[cut:]
	if text == "~" {
		return "~/"
	}

	lookIn := base
	if typedDir != "" {
		lookIn = expand(base, typedDir)
	}
	entries, err := os.ReadDir(lookIn)
	if err != nil {
		return text
	}

	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, partial) || strings.HasPrefix(name, ".") && !strings.HasPrefix(partial, ".") {
			continue
		}
		// Follow symlinks: a link to a directory is as good as one.
		if st, err := os.Stat(filepath.Join(lookIn, name)); err == nil && st.IsDir() {
			matches = append(matches, name)
		}
	}

	switch len(matches) {
	case 0:
		return text
	case 1:
		return typedDir + matches[0] + "/"
	}
	common := matches[0]
	for _, name := range matches[1:] {
		for !strings.HasPrefix(name, common) {
			common = common[:len(common)-1]
		}
	}
	return typedDir + common
}
