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

package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repo creates a work tree with one commit.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks so paths compare equal to what git reports.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "kept.txt", "one\ntwo\nthree\n")
	writeFile(t, dir, "gone.txt", "bye\n")
	writeFile(t, dir, "old name.txt", "same content that stays\nacross the rename\n")
	writeFile(t, dir, ".gitignore", "ignored/\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAndDiff(t *testing.T) {
	dir := repo(t)
	writeFile(t, dir, "kept.txt", "one\n2\nthree\nfour\n")
	writeFile(t, dir, "sub/new.txt", "a\nb\n")
	writeFile(t, dir, "ignored/x.txt", "x\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "mv", "old name.txt", "new name.txt")

	changes, err := Status(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Change{}
	for _, c := range changes {
		got[c.Path] = c
	}

	want := []struct {
		path           string
		mark           string
		added, deleted int
	}{
		{"kept.txt", "M", 2, 1},
		{"sub/new.txt", "?", 2, 0},
		{"gone.txt", "D", 0, 1},
		{"new name.txt", "R", 0, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v", changes)
	}
	for _, w := range want {
		c, ok := got[w.path]
		if !ok {
			t.Errorf("%s missing from %+v", w.path, changes)
			continue
		}
		if c.Mark() != w.mark || c.Added != w.added || c.Deleted != w.deleted {
			t.Errorf("%s = %s +%d -%d; want %s +%d -%d", w.path, c.Mark(), c.Added, c.Deleted, w.mark, w.added, w.deleted)
		}
	}

	diff, err := Diff(dir, got["kept.txt"], "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "-two\n+2\n") || !strings.Contains(diff, "+four") {
		t.Errorf("tracked diff:\n%s", diff)
	}

	diff, err = Diff(dir, got["sub/new.txt"], "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+a\n+b\n") {
		t.Errorf("untracked diff:\n%s", diff)
	}
}

func TestStatusSinceACommit(t *testing.T) {
	dir := repo(t)
	base, ok := Head(dir)
	if !ok || !Exists(dir, base) || Exists(dir, "0000000000000000000000000000000000000001") {
		t.Fatalf("Head = %q, %v", base, ok)
	}

	// Work that is committed, work that is not, and a new file.
	writeFile(t, dir, "kept.txt", "one\n2\nthree\n")
	gitCmd(t, dir, "mv", "old name.txt", "new name.txt")
	gitCmd(t, dir, "commit", "-q", "-a", "-m", "second")
	writeFile(t, dir, "kept.txt", "one\n2\nthree\nfour\n")
	writeFile(t, dir, "sub/new.txt", "a\n")
	if n := Commits(dir, base); n != 1 {
		t.Errorf("Commits = %d, want 1", n)
	}

	// Without a base only what is not committed counts.
	changes, err := Status(dir, "")
	if err != nil || len(changes) != 2 {
		t.Fatalf("uncommitted = %+v, %v", changes, err)
	}

	changes, err = Status(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Change{}
	for _, c := range changes {
		got[c.Path] = c
	}
	if len(got) != 4 {
		t.Fatalf("since the base: %+v", changes)
	}
	if c := got["kept.txt"]; c.Mark() != "M" || c.Added != 2 || c.Deleted != 1 {
		t.Errorf("kept.txt = %+v", c)
	}
	if c := got["old name.txt"]; c.Mark() != "D" || c.Deleted != 2 {
		t.Errorf("old name.txt = %+v", c)
	}
	if c := got["new name.txt"]; c.Mark() != "A" || c.Added != 2 {
		t.Errorf("new name.txt = %+v", c)
	}
	if c := got["sub/new.txt"]; !c.Untracked() || c.Added != 1 {
		t.Errorf("sub/new.txt = %+v", c)
	}

	// The diff spans the commit and the work on top of it.
	diff, err := Diff(dir, got["kept.txt"], base)
	if err != nil || !strings.Contains(diff, "-two") || !strings.Contains(diff, "+four") {
		t.Errorf("diff since the base = %q, %v", diff, err)
	}
	diff, err = Diff(dir, got["kept.txt"], "")
	if err != nil || strings.Contains(diff, "-two") || !strings.Contains(diff, "+four") {
		t.Errorf("diff of what is not committed = %q, %v", diff, err)
	}
}

func TestStatusFromSubdirectory(t *testing.T) {
	dir := repo(t)
	writeFile(t, dir, "sub/new.txt", "a\n")
	writeFile(t, dir, "kept.txt", "changed\n")

	changes, err := Status(filepath.Join(dir, "sub"), "")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	if !slices.Equal(paths, []string{"../kept.txt", "new.txt"}) {
		t.Errorf("paths = %v", paths)
	}
}

func TestStatusBeforeFirstCommit(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	writeFile(t, dir, "a.txt", "x\ny\n")
	gitCmd(t, dir, "add", "a.txt")

	changes, err := Status(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Mark() != "A" || changes[0].Added != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	diff, err := Diff(dir, changes[0], "")
	if err != nil || !strings.Contains(diff, "+x\n+y\n") {
		t.Errorf("diff = %q, %v", diff, err)
	}
}

func TestBranch(t *testing.T) {
	// A branch has its name before its first commit.
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "trunk")
	if got := Branch(dir); got != "trunk" {
		t.Errorf("branch before the first commit = %q", got)
	}

	dir = repo(t)
	gitCmd(t, dir, "switch", "-q", "-c", "feature/login")
	if got := Branch(dir); got != "feature/login" {
		t.Errorf("branch = %q", got)
	}

	// On no branch, the commit is what there is to say.
	gitCmd(t, dir, "switch", "-q", "--detach")
	head, _ := Head(dir)
	if got := Branch(dir); len(got) < 4 || !strings.HasPrefix(head, got) {
		t.Errorf("detached = %q, head %s", got, head)
	}

	if got := Branch(t.TempDir()); got != "" {
		t.Errorf("branch outside a repository = %q", got)
	}
}

func TestStatusOutsideRepository(t *testing.T) {
	if _, err := Status(t.TempDir(), ""); err == nil {
		t.Error("Status succeeded outside a repository")
	}
}

func TestFiles(t *testing.T) {
	dir := repo(t)
	writeFile(t, dir, "sub/new.txt", "a\n")
	writeFile(t, dir, "ignored/x.txt", "x\n")

	files, err := Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "gone.txt", "kept.txt", "old name.txt", "sub/new.txt"}
	if !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}

	// What git ignores is listed apart, a directory without what it holds
	// until it is open.
	writeFile(t, dir, "ignored/deep/y.txt", "y\n")
	if got := Ignored(dir, nil); !slices.Equal(got, []string{"ignored/"}) {
		t.Errorf("ignored = %v", got)
	}
	want = []string{"ignored/", "ignored/deep/", "ignored/x.txt"}
	if got := Ignored(dir, []string{"ignored"}); !slices.Equal(got, want) {
		t.Errorf("ignored, open = %v, want %v", got, want)
	}
	want = []string{"ignored/", "ignored/deep/", "ignored/deep/y.txt", "ignored/x.txt"}
	if got := Ignored(dir, []string{"ignored", "ignored/deep"}); !slices.Equal(got, want) {
		t.Errorf("ignored, both open = %v, want %v", got, want)
	}

	plain := t.TempDir()
	writeFile(t, plain, "a.txt", "")
	writeFile(t, plain, "d/b.txt", "")
	writeFile(t, plain, ".hidden/c.txt", "")
	files, err = Files(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"a.txt", "d/b.txt"}) {
		t.Errorf("files outside a repository = %v", files)
	}
	if got := Ignored(plain, nil); got != nil {
		t.Errorf("ignored outside a repository = %v", got)
	}
}

func TestWorktree(t *testing.T) {
	dir := repo(t)
	path := filepath.Join(dir+".worktrees", "fix")
	if err := AddWorktree(dir, path); err != nil {
		t.Fatal(err)
	}
	// It is on the commit dir is on, and on no branch.
	if _, err := run(path, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		t.Error("the work tree is on a branch")
	}
	if got, want := Branch(path), Branch(dir); got == want || !Exists(dir, got) {
		t.Errorf("commit = %q, branch of dir = %q", got, want)
	}
	if !OnBranch(path) {
		t.Error("the commit it starts from is on no branch")
	}
	// The main work tree is the same seen from either.
	for _, from := range []string{dir, path} {
		if got, ok := Main(from); !ok || got != dir {
			t.Errorf("main of %s = %q, %v", from, got, ok)
		}
	}
	if _, ok := Main(t.TempDir()); ok {
		t.Error("main outside a repository")
	}
	// A path that is taken is refused.
	if err := AddWorktree(dir, path); err == nil {
		t.Error("the work tree was made twice")
	}

	// Work that is not committed keeps the work tree.
	writeFile(t, path, "new.txt", "x\n")
	if Clean(path) {
		t.Error("clean with a new file")
	}
	if err := RemoveWorktree(path); err == nil {
		t.Fatal("removed with a new file in it")
	}
	gitCmd(t, path, "add", "-A")
	gitCmd(t, path, "commit", "-q", "-m", "work")
	if !Clean(path) {
		t.Error("not clean after the commit")
	}
	// A commit on no branch would go with the work tree.
	if OnBranch(path) {
		t.Error("a commit on no branch is on one")
	}
	gitCmd(t, path, "switch", "-q", "-c", "fix")
	if !OnBranch(path) {
		t.Error("a commit on a branch is on none")
	}
	if err := RemoveWorktree(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the work tree is still there")
	}
	// Its branch, and so the commit, stays.
	if !Exists(dir, "fix") {
		t.Error("the branch went with the work tree")
	}
}

func TestParseLog(t *testing.T) {
	out := "aaaa\x1faa\x1f1700000000\x1fsecond: with \x1f in it\x00bbbb\x1fbb\x1f1690000000\x1ffirst\x00"
	got := parseLog([]byte(out))
	if len(got) != 2 {
		t.Fatalf("got %d commits: %+v", len(got), got)
	}
	if got[0].Hash != "aaaa" || got[0].Short != "aa" || got[0].Subject != "second: with \x1f in it" || got[0].When.Unix() != 1700000000 {
		t.Errorf("first record: %+v", got[0])
	}
	if got[1].Subject != "first" {
		t.Errorf("second record: %+v", got[1])
	}
	if parseLog(nil) != nil {
		t.Error("no output should give no commits")
	}
}

func TestLogAndCommits(t *testing.T) {
	dir := repo(t)
	base, _ := Head(dir)
	writeFile(t, dir, "kept.txt", "one\ntwo\nthree\nfour\n")
	gitCmd(t, dir, "commit", "-q", "-am", "add four")
	writeFile(t, dir, "sub/new.txt", "hello\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "add new")

	log, err := Log(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Subject != "add new" || log[1].Subject != "add four" {
		t.Fatalf("log: %+v", log)
	}
	if log[0].Short == "" || !strings.HasPrefix(log[0].Hash, log[0].Short) || log[0].When.IsZero() {
		t.Errorf("fields: %+v", log[0])
	}

	// A commit lists only its own files, with paths from the directory asked.
	changes, err := CommitStatus(filepath.Join(dir, "sub"), log[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "new.txt" || changes[0].Mark() != "A" || changes[0].Added != 1 {
		t.Errorf("changes of the last commit: %+v", changes)
	}
	changes, err = CommitStatus(dir, log[1].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "kept.txt" || changes[0].Mark() != "M" || changes[0].Added != 1 {
		t.Errorf("changes of the first commit: %+v", changes)
	}
	diff, err := CommitDiff(dir, log[1].Hash, "kept.txt")
	if err != nil || !strings.Contains(diff, "+four") || strings.Contains(diff, "new.txt") {
		t.Errorf("diff of kept.txt: %v\n%s", err, diff)
	}

	// The first commit of a repository is compared against nothing.
	changes, err = CommitStatus(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 || changes[0].Mark() != "A" {
		t.Errorf("changes of the root commit: %+v", changes)
	}
	if diff, err := CommitDiff(dir, base, "gone.txt"); err != nil || !strings.Contains(diff, "+bye") {
		t.Errorf("diff in the root commit: %v\n%s", err, diff)
	}

	if log, err := Log(dir, "HEAD"); err != nil || len(log) != 0 {
		t.Errorf("nothing since HEAD: %v %+v", err, log)
	}
}
