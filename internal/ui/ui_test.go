package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFit(t *testing.T) {
	for _, tt := range []struct {
		in    string
		width int
		want  string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"abc", 3, "abc"},
		{"abc", 0, ""},
		{"日本語", 5, "日本 "},
	} {
		got := Fit(tt.in, tt.width)
		if tt.width > 0 && ansi.StringWidth(got) != tt.width {
			t.Errorf("Fit(%q, %d) is %d cells wide", tt.in, tt.width, ansi.StringWidth(got))
		}
		if tt.in != "日本語" && got != tt.want {
			t.Errorf("Fit(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}

	styled := Fit(Bad.Render("abcdef"), 4)
	if ansi.StringWidth(styled) != 4 || ansi.Strip(styled) != "abc…" {
		t.Errorf("styled Fit = %q", styled)
	}
}

func TestBoxSize(t *testing.T) {
	box := Box("Title", []string{"one", "a line that is far too long for the box"}, 12, 5, true)
	if len(box) != 5 {
		t.Fatalf("box has %d rows", len(box))
	}
	for i, row := range box {
		if w := ansi.StringWidth(row); w != 12 {
			t.Errorf("row %d is %d cells wide: %q", i, w, ansi.Strip(row))
		}
	}
	if !strings.Contains(ansi.Strip(box[0]), "Title") {
		t.Errorf("top border = %q", ansi.Strip(box[0]))
	}
}

func paths(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}

func TestTree(t *testing.T) {
	tr := NewTree()
	tr.SetFiles([]string{"README.md", "api/auth.go", "api/v1/users.go", "main.go", "zeta/z.go"})

	// Directories come first and start closed.
	if got := paths(tr.Rows()); !slices.Equal(got, []string{"api", "zeta", "README.md", "main.go"}) {
		t.Fatalf("closed tree = %v", got)
	}

	tr.SetOpen(true)
	if got := paths(tr.Rows()); !slices.Equal(got, []string{"api", "api/v1", "api/auth.go", "zeta", "README.md", "main.go"}) {
		t.Fatalf("api open = %v", got)
	}
	if r := tr.Rows()[2]; r.Depth != 1 || r.Name != "auth.go" || r.Dir {
		t.Errorf("row = %+v", r)
	}

	// Closing from a file inside goes to the parent, then closes it.
	tr.Move(2)
	tr.SetOpen(false)
	if tr.Selected().Path != "api" {
		t.Errorf("cursor after left on a file = %q", tr.Selected().Path)
	}
	tr.SetOpen(false)
	if len(tr.Rows()) != 4 {
		t.Errorf("api still open: %v", paths(tr.Rows()))
	}

	tr.Reveal("api/v1/users.go")
	if tr.Selected().Path != "api/v1/users.go" {
		t.Errorf("Reveal put the cursor on %q", tr.Selected().Path)
	}

	// The cursor follows its file when the list changes around it.
	tr.SetFiles([]string{"README.md", "aaa.go", "api/auth.go", "api/v1/users.go", "main.go"})
	if tr.Selected().Path != "api/v1/users.go" {
		t.Errorf("cursor moved to %q after a refresh", tr.Selected().Path)
	}
}

func TestFuzzy(t *testing.T) {
	if _, ok := Fuzzy("xyz", "internal/app/app.go"); ok {
		t.Error("matched characters that are not there")
	}
	if _, ok := Fuzzy("APP", "internal/app/app.go"); !ok {
		t.Error("match should ignore case")
	}

	rank := func(query string, texts ...string) string {
		best, bestScore := "", -1<<31
		for _, text := range texts {
			if s, ok := Fuzzy(query, text); ok && s > bestScore {
				best, bestScore = text, s
			}
		}
		return best
	}
	if got := rank("auth", "api/a_u_t_h.go", "api/auth.go"); got != "api/auth.go" {
		t.Errorf("consecutive match should win, got %q", got)
	}
	if got := rank("main", "cmd/cicoria/main.go", "main.go"); got != "main.go" {
		t.Errorf("shorter text should win a tie, got %q", got)
	}
}

func TestDiffLines(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+2\n@@ -9 +9 @@\n-a\n+b\n"
	lines, hunks := DiffLines(diff)
	if len(lines) != 10 || !slices.Equal(hunks, []int{3, 7}) {
		t.Fatalf("%d lines, hunks %v", len(lines), hunks)
	}
	if ansi.Strip(lines[5]) != "-two" || lines[5] == "-two" {
		t.Errorf("removed line = %q", lines[5])
	}
}

func TestDiffRefs(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n one\n-two\n+2\n@@ -9 +9,2 @@ func x\n-a\n+b\n+--- not a header\n\\ No newline at end of file\n"
	want := []LineRef{
		{}, {}, {}, {},
		{Old: 1, New: 1}, {Old: 2}, {New: 2},
		{},
		{Old: 9}, {New: 9}, {New: 10},
		{},
	}
	if got := DiffRefs(diff); !slices.Equal(got, want) {
		t.Errorf("DiffRefs = %v\nwant      %v", got, want)
	}
	lines, _ := DiffLines(diff)
	if len(lines) != len(want) {
		t.Errorf("%d lines for %d refs", len(lines), len(want))
	}
}

func TestPagerCursor(t *testing.T) {
	var p Pager
	p.SetContent(make([]string, 100), []int{50})
	const h = 20

	// The cursor moves alone until it would leave the screen.
	p.Move(5, h)
	if p.Cursor != 5 || p.Top != 0 {
		t.Errorf("after moving down: cursor %d, top %d", p.Cursor, p.Top)
	}
	p.Move(30, h)
	if p.Cursor != 35 || p.Top != 16 {
		t.Errorf("after moving off screen: cursor %d, top %d", p.Cursor, p.Top)
	}
	// Scrolling takes it along when it would be left behind.
	p.Scroll(10, h)
	if p.Cursor != 35 || p.Top != 26 {
		t.Errorf("after scrolling a little: cursor %d, top %d", p.Cursor, p.Top)
	}
	p.Scroll(30, h)
	if p.Cursor != 56 || p.Top != 56 {
		t.Errorf("after scrolling a lot: cursor %d, top %d", p.Cursor, p.Top)
	}
	p.Move(1000, h)
	if p.Cursor != 99 || p.Top != 80 {
		t.Errorf("at the end: cursor %d, top %d", p.Cursor, p.Top)
	}
	p.Mark(-1)
	if p.Cursor != 50 || p.Top != 50 {
		t.Errorf("at the mark: cursor %d, top %d", p.Cursor, p.Top)
	}

	// A selection runs from where it started to the cursor, either way.
	if from, to := p.Selection(); from != 50 || to != 50 {
		t.Errorf("no selection = %d-%d", from, to)
	}
	p.Select()
	p.Move(-3, h)
	if from, to := p.Selection(); from != 47 || to != 50 || !p.Selecting() {
		t.Errorf("selection upwards = %d-%d", from, to)
	}
	p.Move(5, h)
	if from, to := p.Selection(); from != 50 || to != 52 {
		t.Errorf("selection downwards = %d-%d", from, to)
	}
	p.Noted = map[int]bool{51: true}
	rows := p.View(40, h)
	if got := ansi.Strip(rows[51-p.Top]); !strings.HasPrefix(got, "*▌") {
		t.Errorf("a selected line with a note = %q", got)
	}
	if got := ansi.Strip(rows[53-p.Top]); !strings.HasPrefix(got, "  ") {
		t.Errorf("a line outside the selection = %q", got)
	}
	p.Unselect()
	if from, to := p.Selection(); from != 52 || to != 52 {
		t.Errorf("after dropping the selection = %d-%d", from, to)
	}

	// An empty text has nothing to stand on, and nothing breaks.
	p.SetContent(nil, nil)
	p.Move(3, h)
	p.Scroll(3, h)
	if p.Cursor != 0 || p.Top != 0 || len(p.View(10, h)) != h {
		t.Errorf("empty: cursor %d, top %d", p.Cursor, p.Top)
	}
}

func TestFileLines(t *testing.T) {
	lines := FileLines("main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n", true)
	if len(lines) != 5 {
		t.Fatalf("%d lines", len(lines))
	}
	if got := ansi.Strip(lines[3]); got != `4     println("hi")` {
		t.Errorf("line 4 = %q", got)
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Error("no colours for a known language")
	}

	// A block comment keeps its colour on every line it spans.
	lines = FileLines("x.go", "/* a\nb */\n", true)
	if len(lines) != 2 || !strings.Contains(strings.SplitN(lines[1], " ", 2)[1], "\x1b[") {
		t.Errorf("multi-line token: %q", lines)
	}

	if got := FileLines("notes.unknownext", "plain\ntext", true); len(got) != 2 || ansi.Strip(got[1]) != "2 text" {
		t.Errorf("unknown language: %q", got)
	}
}

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("hello è\n")) {
		t.Error("text taken for binary")
	}
	if !IsBinary([]byte{0x7f, 'E', 'L', 'F', 0, 1, 2}) {
		t.Error("binary taken for text")
	}
}

func TestPager(t *testing.T) {
	var p Pager
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	lines[40], lines[70] = "needle one", Bad.Render("NEEDLE two")
	p.SetContent(lines, []int{10, 50})

	p.Scroll(1000, 20)
	if p.Top != 80 {
		t.Errorf("scrolled past the end: top = %d", p.Top)
	}
	p.Scroll(-1000, 20)
	if p.Top != 0 {
		t.Errorf("scrolled past the start: top = %d", p.Top)
	}

	if !p.Mark(1) || p.Top != 10 || !p.Mark(1) || p.Top != 50 || p.Mark(1) {
		t.Errorf("marks forward ended at %d", p.Top)
	}
	if !p.Mark(-1) || p.Top != 10 {
		t.Errorf("mark backward went to %d", p.Top)
	}

	p.Search("needle")
	if p.Top != 40 {
		t.Errorf("search went to %d", p.Top)
	}
	p.NextMatch(1)
	if p.Top != 70 {
		t.Errorf("next match went to %d", p.Top)
	}
	p.NextMatch(1)
	if p.Top != 40 {
		t.Errorf("search did not wrap, at %d", p.Top)
	}

	view := p.View(30, 20)
	if len(view) != 20 || ansi.StringWidth(view[0]) != 30 {
		t.Errorf("view is %d rows of %d cells", len(view), ansi.StringWidth(view[0]))
	}
}

func TestInputEditing(t *testing.T) {
	key := func(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }
	typed := func(in *Input, text string) {
		for _, r := range text {
			in.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	var in Input
	typed(&in, "run the tests")

	// The arrows move the cursor, and typing goes where it is.
	for range 5 {
		in.Key(key(tea.KeyLeft, 0))
	}
	typed(&in, "all ")
	if in.Value() != "run the all tests" {
		t.Errorf("after typing in the middle: %q", in.Value())
	}
	if got := ansi.Strip(in.View()); got != "run the all tests" {
		t.Errorf("view with the cursor on a letter = %q", got)
	}

	// ctrl+w takes the word before the cursor, backspace and delete one
	// character on either side of it.
	in.Key(key('w', tea.ModCtrl))
	if in.Value() != "run the tests" {
		t.Errorf("after ctrl+w: %q", in.Value())
	}
	in.Key(key(tea.KeyBackspace, 0))
	in.Key(key(tea.KeyDelete, 0))
	if in.Value() != "run theests" {
		t.Errorf("after backspace and delete: %q", in.Value())
	}

	// Home and end, and the words between them.
	in.Key(key(tea.KeyHome, 0))
	typed(&in, "> ")
	in.Key(key(tea.KeyRight, tea.ModCtrl))
	typed(&in, "!")
	in.Key(key(tea.KeyEnd, 0))
	typed(&in, ".")
	if in.Value() != "> run! theests." {
		t.Errorf("after home, a word and end: %q", in.Value())
	}

	// ctrl+k and ctrl+u cut to the end and to the start.
	in.Key(key(tea.KeyLeft, tea.ModCtrl))
	in.Key(key('k', tea.ModCtrl))
	if in.Value() != "> run! " {
		t.Errorf("after ctrl+k: %q", in.Value())
	}
	in.Key(key(tea.KeyLeft, 0))
	in.Key(key('u', tea.ModCtrl))
	if in.Value() != " " {
		t.Errorf("after ctrl+u: %q", in.Value())
	}

	// At the edges nothing happens, and nothing breaks.
	in.Set("")
	for _, k := range []tea.KeyPressMsg{key(tea.KeyLeft, 0), key(tea.KeyRight, 0), key(tea.KeyBackspace, 0), key(tea.KeyDelete, 0), key('w', tea.ModCtrl)} {
		if in.Key(k) {
			t.Errorf("%s changed an empty field", k.String())
		}
	}
}
