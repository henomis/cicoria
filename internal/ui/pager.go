package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Pager shows styled text that is longer than the screen.
type Pager struct {
	// Title is shown in the header.
	Title string
	// lines are what is drawn; plain are the same lines without styling, for
	// searching.
	lines []string
	plain []string
	// marks are the lines that "next/previous mark" jumps between.
	marks []int

	Top int
	// Cursor is the line the user is on. It stays on screen.
	Cursor int
	// Noted says which lines carry a mark in the gutter.
	Noted map[int]bool
	// selecting says a selection runs from anchor to the cursor.
	selecting bool
	anchor    int

	query   string
	matches []int
	match   int
}

// SetContent replaces the text. marks may be nil.
func (p *Pager) SetContent(lines []string, marks []int) {
	p.lines = lines
	p.marks = marks
	p.plain = make([]string, len(lines))
	for i, l := range lines {
		p.plain[i] = strings.ToLower(ansi.Strip(l))
	}
	p.Top, p.Cursor, p.selecting = 0, 0, false
	p.Search(p.query)
}

// Len returns the number of lines.
func (p *Pager) Len() int { return len(p.lines) }

// clamp keeps the scroll position within the text for a body of height rows,
// and the cursor on a line that is shown.
func (p *Pager) clamp(height int) {
	p.Top = max(min(p.Top, len(p.lines)-height), 0)
	p.Cursor = max(min(p.Cursor, p.Top+height-1, len(p.lines)-1), p.Top)
}

// Scroll moves the text by delta lines. The cursor follows when it would
// be left behind.
func (p *Pager) Scroll(delta, height int) {
	p.Top += delta
	p.clamp(height)
}

// Move moves the cursor by delta lines, and the text with it when it would
// leave the screen.
func (p *Pager) Move(delta, height int) {
	p.SetCursor(p.Cursor+delta, height)
}

// SetCursor puts the cursor on a line.
func (p *Pager) SetCursor(line, height int) {
	line = max(min(line, len(p.lines)-1), 0)
	if line < p.Top {
		p.Top = line
	}
	if line >= p.Top+height {
		p.Top = line - height + 1
	}
	p.Cursor = line
	p.clamp(height)
}

// jump shows a line at the top, with the cursor on it.
func (p *Pager) jump(line int) {
	p.Top, p.Cursor = line, line
}

// Select starts a selection at the cursor, or drops the one there is. It
// reports whether there is one now.
func (p *Pager) Select() bool {
	p.selecting, p.anchor = !p.selecting, p.Cursor
	return p.selecting
}

// Selecting reports whether more than the cursor line may be selected.
func (p *Pager) Selecting() bool { return p.selecting }

// Unselect drops the selection.
func (p *Pager) Unselect() { p.selecting = false }

// Selection returns the first and last selected line: the cursor line when
// nothing else is selected.
func (p *Pager) Selection() (from, to int) {
	if !p.selecting {
		return p.Cursor, p.Cursor
	}
	anchor := max(min(p.anchor, len(p.lines)-1), 0)
	return min(anchor, p.Cursor), max(anchor, p.Cursor)
}

// Mark jumps to the next (dir > 0) or previous mark. It reports whether
// there was one to jump to.
func (p *Pager) Mark(dir int) bool {
	if dir > 0 {
		for _, m := range p.marks {
			if m > p.Top {
				p.jump(m)
				return true
			}
		}
		return false
	}
	for i := len(p.marks) - 1; i >= 0; i-- {
		if p.marks[i] < p.Top {
			p.jump(p.marks[i])
			return true
		}
	}
	return false
}

// Search finds the lines containing query, ignoring case, and jumps to the
// first one at or after the current position.
func (p *Pager) Search(query string) {
	p.query = query
	p.matches = nil
	p.match = 0
	if query == "" {
		return
	}
	needle := strings.ToLower(query)
	for i, l := range p.plain {
		if strings.Contains(l, needle) {
			p.matches = append(p.matches, i)
		}
	}
	for i, m := range p.matches {
		if m >= p.Top {
			p.match = i
			p.jump(m)
			return
		}
	}
	if len(p.matches) > 0 {
		p.jump(p.matches[0])
	}
}

// NextMatch jumps to the next (dir > 0) or previous search match, wrapping
// around.
func (p *Pager) NextMatch(dir int) {
	if len(p.matches) == 0 {
		return
	}
	p.match = (p.match + dir + len(p.matches)) % len(p.matches)
	p.jump(p.matches[p.match])
}

// Status describes the position in the text and the state of the search.
func (p *Pager) Status(height int) string {
	s := "empty"
	if n := len(p.lines); n > 0 {
		s = fmt.Sprintf("%d-%d/%d", p.Top+1, min(p.Top+height, n), n)
	}
	if p.query != "" {
		if len(p.matches) == 0 {
			s += fmt.Sprintf(" · no match for %q", p.query)
		} else {
			s += fmt.Sprintf(" · match %d/%d for %q", p.match+1, len(p.matches), p.query)
		}
	}
	return s
}

// View renders height rows of the text, width cells wide.
func (p *Pager) View(width, height int) []string {
	p.clamp(height)
	end := min(p.Top+height, len(p.lines))

	isMatch := map[int]bool{}
	for _, m := range p.matches {
		isMatch[m] = true
	}

	// The gutter has a column for the lines that carry a note, and one for
	// the cursor, the selection and the search matches.
	from, to := p.Selection()
	rows := make([]string, 0, height)
	for i := p.Top; i < end; i++ {
		note, bar := " ", " "
		if p.Noted[i] {
			note = Warn.Render("*")
		}
		switch {
		case i >= from && i <= to:
			bar = Accent.Render("▌")
		case isMatch[i]:
			bar = Warn.Render("▌")
		}
		rows = append(rows, note+bar+p.lines[i])
	}
	return Block(rows, width, height)
}
