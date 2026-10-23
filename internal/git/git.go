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
	"time"
)

// maxWalk bounds how many files are listed outside of a repository, and how
// many ignored ones in one.
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

// Head returns the commit the work tree containing dir is on, and whether
// there is one.
func Head(dir string) (string, bool) {
	out, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Branch returns the branch the work tree containing dir is on, or the short
// name of its commit when it is on none. It is empty outside a repository.
func Branch(dir string) string {
	out, err := run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		out, err = run(dir, "rev-parse", "--verify", "--quiet", "--short", "HEAD")
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Exists reports whether a commit is still in the repository of dir.
func Exists(dir, commit string) bool {
	_, err := run(dir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// Commits counts the commits made since base on the branch dir is on.
func Commits(dir, base string) int {
	out, err := run(dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// Commit is one commit of the history.
type Commit struct {
	Hash, Short, Subject string
	When                 time.Time
}

// maxLog bounds how many commits Log returns.
const maxLog = 200

// Log lists the commits made since base on the branch dir is on, the newest
// first.
func Log(dir, base string) ([]Commit, error) {
	out, err := run(dir, "log", "-z", fmt.Sprintf("--max-count=%d", maxLog), "--format=%H%x1f%h%x1f%ct%x1f%s", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// parseLog reads the output of Log's "git log": one record per commit, its
// fields apart by a unit separator.
func parseLog(out []byte) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(strings.TrimPrefix(rec, "\n"), "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		c := Commit{Hash: f[0], Short: f[1], Subject: f[3]}
		if sec, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			c.When = time.Unix(sec, 0)
		}
		commits = append(commits, c)
	}
	return commits
}

// parent returns what a commit is compared against: its first parent, or the
// empty tree for the first commit of a repository.
func parent(dir, hash string) string {
	if _, err := run(dir, "rev-parse", "--verify", "--quiet", hash+"^1"); err == nil {
		return hash + "^1"
	}
	// With nothing on its standard input, this names the empty tree in the
	// repository's own hash.
	if out, err := run(dir, "hash-object", "-t", "tree", "--stdin"); err == nil {
		return strings.TrimSpace(string(out))
	}
	return hash + "^1"
}

// CommitStatus lists the files a commit changed, as Status does for the work
// tree.
func CommitStatus(dir, hash string) ([]Change, error) {
	root, ok := Root(dir)
	if !ok {
		return nil, errors.New("not a git repository")
	}
	against := []string{parent(dir, hash), hash, "--no-renames"}
	out, err := run(dir, append([]string{"diff", "--name-status", "-z"}, against...)...)
	if err != nil {
		return nil, err
	}
	changes := parseNameStatus(out)
	count(dir, root, changes, against)
	return changes, nil
}

// CommitDiff returns the unified diff of a commit, or of the files of it
// that are named.
func CommitDiff(dir, hash string, paths ...string) (string, error) {
	out, err := run(dir, append([]string{"diff", parent(dir, hash), hash, "--"}, paths...)...)
	return string(out), err
}

// Status lists the changed files of the work tree containing dir: what is
// not committed or, when base is a commit, everything that is different
// from it, committed or not.
func Status(dir, base string) ([]Change, error) {
	root, ok := Root(dir)
	if !ok {
		return nil, errors.New("not a git repository")
	}

	out, err := run(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	changes := parseStatus(out)

	// A rename against a commit is told as a file gone and a file come, so
	// that every path stands for itself.
	against := []string{base, "--no-renames"}
	if base == "" {
		against = []string{"HEAD"}
		if !hasHead(dir) {
			against = []string{"--cached"}
		}
	} else {
		out, err := run(dir, append([]string{"diff", "--name-status", "-z"}, against...)...)
		if err != nil {
			return nil, err
		}
		changes = append(parseNameStatus(out), slices.DeleteFunc(changes, func(c Change) bool { return !c.Untracked() })...)
	}
	count(dir, root, changes, against)
	return changes, nil
}

// count fills in the line counts of changes taken against a commit, or two,
// and makes their paths relative to dir, sorted.
func count(dir, root string, changes []Change, against []string) {
	// Line counts are a nicety: without them the list is still right.
	stats := map[string][2]int{}
	if out, err := run(dir, append([]string{"diff", "--numstat", "-z"}, against...)...); err == nil {
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

// parseNameStatus reads the output of "git diff --name-status -z" taken
// without renames: a letter and a path, in turn.
func parseNameStatus(out []byte) []Change {
	var changes []Change
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] != "" && fields[i+1] != "" {
			changes = append(changes, Change{Path: fields[i+1], Code: fields[i][:1] + " "})
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

// Diff returns the unified diff of a change reported by Status for the same
// dir and base.
func Diff(dir string, c Change, base string) (string, error) {
	if c.Untracked() {
		// Comparing against nothing shows the whole file as added. git exits
		// with 1 when the two sides differ, which here is always.
		out, err := run(dir, "diff", "--no-index", "--", os.DevNull, c.Path)
		if len(out) > 0 {
			return string(out), nil
		}
		return "", err
	}

	if base == "" {
		base = "HEAD"
		if !hasHead(dir) {
			base = "--cached"
		}
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

// Ignored lists what git ignores under dir, relative to it and sorted. A
// directory ends in a slash and is not looked into, as it may hold a great
// many files, unless it is one of open: then what is directly inside it is
// listed as well.
func Ignored(dir string, open []string) []string {
	out, err := run(dir, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil
	}
	isOpen := map[string]bool{}
	for _, d := range open {
		isOpen[d] = true
	}

	var paths []string
	var add func(p string)
	add = func(p string) {
		if len(paths) >= maxWalk {
			return
		}
		paths = append(paths, p)
		d := strings.TrimSuffix(p, "/")
		if d == p || !isOpen[d] {
			return
		}
		entries, _ := os.ReadDir(filepath.Join(dir, filepath.FromSlash(d)))
		for _, e := range entries {
			child := d + "/" + e.Name()
			if e.IsDir() {
				child += "/"
			}
			add(child)
		}
	}
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			add(p)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// Main returns the main work tree of the repository of dir: the one the
// others were added to. It is the first that git lists.
func Main(dir string) (string, bool) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(out), "\n")
	path, ok := strings.CutPrefix(first, "worktree ")
	return path, ok && path != ""
}

// AddWorktree adds a work tree at path to the repository of dir, on the
// commit dir is on and on no branch: making one is up to whoever works there.
func AddWorktree(dir, path string) error {
	_, err := run(dir, "worktree", "add", "-q", "--detach", path)
	return err
}

// RemoveWorktree takes the work tree at path away from its repository. git
// refuses when it holds changes that are not committed, and a branch it is on
// is left alone.
func RemoveWorktree(path string) error {
	main, ok := Main(path)
	if !ok {
		return errors.New("not a git repository")
	}
	_, err := run(main, "worktree", "remove", path)
	return err
}

// OnBranch reports whether the commit the work tree containing dir is on is
// on a branch, so that it outlives the work tree.
func OnBranch(dir string) bool {
	out, err := run(dir, "for-each-ref", "--count=1", "--contains=HEAD", "refs/heads")
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// Clean reports whether the work tree containing dir has nothing that is
// not committed.
func Clean(dir string) bool {
	out, err := run(dir, "status", "--porcelain")
	return err == nil && len(bytes.TrimSpace(out)) == 0
}
