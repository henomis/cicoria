// Package git reads the state of a work tree through the git command line.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// maxWalk bounds how many files are listed outside of a repository.
const maxWalk = 5000

// Change is one changed file in the work tree.
type Change struct {
	// Path is relative to the directory the status was asked for.
	Path string
	// Code is git's two-letter status, e.g. " M", "A ", "??".
	Code string
	// Added and Deleted count changed lines against HEAD.
	Added, Deleted int
}

// Untracked reports whether the file is new and not yet added.
func (c Change) Untracked() bool { return c.Code == "??" }

// Mark is the single letter shown for the change.
func (c Change) Mark() string {
	switch {
	case c.Untracked():
		return "?"
	case strings.Contains(c.Code, "U"):
		return "U"
	case strings.Contains(c.Code, "D"):
		return "D"
	case strings.Contains(c.Code, "A"):
		return "A"
	case strings.Contains(c.Code, "R"):
		return "R"
	}
	return "M"
}

// run executes git in dir and returns its standard output.
func run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("git %s: %s", args[0], msg)
		}
		return out, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// Root returns the top of the work tree containing dir, and whether dir is
// in a work tree at all.
func Root(dir string) (string, bool) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func hasHead(dir string) bool {
	_, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// Status lists the changed files of the work tree containing dir.
func Status(dir string) ([]Change, error) {
	root, ok := Root(dir)
	if !ok {
		return nil, errors.New("not a git repository")
	}

	out, err := run(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	changes := parseStatus(out)

	base := "HEAD"
	if !hasHead(dir) {
		base = "--cached"
	}
	// Line counts are a nicety: without them the list is still right.
	stats := map[string][2]int{}
	if out, err := run(dir, "diff", "--numstat", "-z", base); err == nil {
		stats = parseNumstat(out)
	}

	for i := range changes {
		c := &changes[i]
		if c.Untracked() {
			c.Added = countLines(filepath.Join(root, c.Path))
		} else if s, ok := stats[c.Path]; ok {
			c.Added, c.Deleted = s[0], s[1]
		}
		// git reports paths from the top of the work tree.
		if rel, err := filepath.Rel(dir, filepath.Join(root, c.Path)); err == nil {
			c.Path = rel
		}
	}

	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

// parseStatus reads the output of "git status --porcelain=v1 -z".
func parseStatus(out []byte) []Change {
	var changes []Change
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		code := f[:2]
		changes = append(changes, Change{Path: f[3:], Code: code})
		// A rename or copy is followed by the path it came from.
		if code[0] == 'R' || code[0] == 'C' {
			i++
		}
	}
	return changes
}

// parseNumstat reads the output of "git diff --numstat -z" into added and
// deleted line counts by path.
func parseNumstat(out []byte) map[string][2]int {
	stats := map[string][2]int{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		// A rename has an empty path here, then the old and new paths.
		if path == "" && i+2 < len(fields) {
			path = fields[i+2]
			i += 2
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		stats[path] = [2]int{added, deleted}
	}
	return stats
}

// countLines counts the lines of a file, giving 0 when it cannot be read or
// is too large to be worth it.
func countLines(path string) int {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > 1<<20 {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// Diff returns the unified diff against HEAD of a change reported by Status
// for the same dir.
func Diff(dir string, c Change) (string, error) {
	if c.Untracked() {
		// Comparing against nothing shows the whole file as added. git exits
		// with 1 when the two sides differ, which here is always.
		out, err := run(dir, "diff", "--no-index", "--", os.DevNull, c.Path)
		if len(out) > 0 {
			return string(out), nil
		}
		return "", err
	}

	base := "HEAD"
	if !hasHead(dir) {
		base = "--cached"
	}
	out, err := run(dir, "diff", base, "--", c.Path)
	return string(out), err
}

// Files lists the files under dir, relative to it and sorted. In a work tree
// it leaves out what git ignores; elsewhere it skips hidden directories.
func Files(dir string) ([]string, error) {
	var files []string

	if _, ok := Root(dir); ok {
		out, err := run(dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		for f := range strings.SplitSeq(string(out), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	} else {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != dir && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if len(files) >= maxWalk {
				return filepath.SkipAll
			}
			if rel, err := filepath.Rel(dir, path); err == nil {
				files = append(files, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	slices.Sort(files)
	return slices.Compact(files), nil
}
