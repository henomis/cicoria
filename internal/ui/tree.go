package ui

import (
	"path"
	"slices"
	"strings"
)

// Row is one visible line of a file tree.
type Row struct {
	// Path is relative to the tree's root, with forward slashes.
	Path  string
	Name  string
	Dir   bool
	Depth int
	Open  bool
}

// Tree is a collapsible view over a flat list of file paths.
type Tree struct {
	files  []string
	open   map[string]bool
	rows   []Row
	Cursor int
	Top    int
}

// NewTree returns an empty tree.
func NewTree() *Tree {
	return &Tree{open: map[string]bool{}}
}

// SetFiles replaces the files shown. The paths must be sorted. The cursor
// stays on the same path when it is still there.
func (t *Tree) SetFiles(files []string) {
	if slices.Equal(files, t.files) {
		return
	}
	selected := t.Selected().Path
	t.files = files
	t.rebuild()
	t.moveTo(selected)
}

// rebuild computes the visible rows from the files and the open directories.
func (t *Tree) rebuild() {
	t.rows = t.rows[:0]
	seen := map[string]bool{}

	type entry struct {
		path string
		dir  bool
	}
	// Collect every directory and file once, then order them so that each
	// directory lists its subdirectories before its files.
	var entries []entry
	for _, f := range t.files {
		for dir := path.Dir(f); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				break
			}
			seen[dir] = true
			entries = append(entries, entry{dir, true})
		}
		entries = append(entries, entry{f, false})
	}
	dirs := map[string]bool{}
	for _, e := range entries {
		if e.dir {
			dirs[e.path] = true
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return comparePaths(a.path, b.path, dirs)
	})

	for _, e := range entries {
		// Shown only when every directory above it is open.
		visible := true
		for dir := path.Dir(e.path); dir != "."; dir = path.Dir(dir) {
			if !t.open[dir] {
				visible = false
				break
			}
		}
		if !visible {
			continue
		}
		t.rows = append(t.rows, Row{
			Path:  e.path,
			Name:  path.Base(e.path),
			Dir:   e.dir,
			Depth: strings.Count(e.path, "/"),
			Open:  e.dir && t.open[e.path],
		})
	}
	t.Cursor = max(min(t.Cursor, len(t.rows)-1), 0)
}

// comparePaths orders two paths as a tree lists them: component by
// component, directories before files, then by name.
func comparePaths(a, b string, dirs map[string]bool) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		aDir := i < len(as)-1 || dirs[a]
		bDir := i < len(bs)-1 || dirs[b]
		if aDir != bDir {
			if aDir {
				return -1
			}
			return 1
		}
		return strings.Compare(as[i], bs[i])
	}
	return len(as) - len(bs)
}

func (t *Tree) moveTo(p string) {
	for i, r := range t.rows {
		if r.Path == p {
			t.Cursor = i
			return
		}
	}
}

// Rows returns the visible rows.
func (t *Tree) Rows() []Row { return t.rows }

// Selected returns the row under the cursor, or a zero Row in an empty tree.
func (t *Tree) Selected() Row {
	if t.Cursor < 0 || t.Cursor >= len(t.rows) {
		return Row{}
	}
	return t.rows[t.Cursor]
}

// Move moves the cursor by delta rows.
func (t *Tree) Move(delta int) {
	t.Cursor = max(min(t.Cursor+delta, len(t.rows)-1), 0)
}

// SetOpen opens or closes the directory under the cursor. Closing a file, or
// a directory that is already closed, moves to its parent instead.
func (t *Tree) SetOpen(open bool) {
	r := t.Selected()
	if r.Path == "" {
		return
	}
	if r.Dir && (open || r.Open) {
		t.open[r.Path] = open
		t.rebuild()
		t.moveTo(r.Path)
		return
	}
	if !open {
		t.moveTo(path.Dir(r.Path))
	}
}

// Reveal opens the directories above a file and puts the cursor on it.
func (t *Tree) Reveal(file string) {
	for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
		t.open[dir] = true
	}
	t.rebuild()
	t.moveTo(file)
}
