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
}
