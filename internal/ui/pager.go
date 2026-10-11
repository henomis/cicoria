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

	Top     int
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
	p.Top = 0
	p.Search(p.query)
}

// Len returns the number of lines.
func (p *Pager) Len() int { return len(p.lines) }

// clamp keeps the scroll position within the text for a body of height rows.
func (p *Pager) clamp(height int) {
	p.Top = max(min(p.Top, len(p.lines)-height), 0)
}

// Scroll moves the text by delta lines.
func (p *Pager) Scroll(delta, height int) {
	p.Top += delta
	p.clamp(height)
}

// Mark jumps to the next (dir > 0) or previous mark. It reports whether
// there was one to jump to.
func (p *Pager) Mark(dir int) bool {
	if dir > 0 {
		for _, m := range p.marks {
			if m > p.Top {
				p.Top = m
				return true
			}
		}
		return false
	}
	for i := len(p.marks) - 1; i >= 0; i-- {
		if p.marks[i] < p.Top {
			p.Top = p.marks[i]
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
			p.Top = m
			return
		}
	}
	if len(p.matches) > 0 {
		p.Top = p.matches[0]
	}
}

// NextMatch jumps to the next (dir > 0) or previous search match, wrapping
// around.
func (p *Pager) NextMatch(dir int) {
	if len(p.matches) == 0 {
		return
	}
	p.match = (p.match + dir + len(p.matches)) % len(p.matches)
	p.Top = p.matches[p.match]
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

	rows := make([]string, 0, height)
	for i := p.Top; i < end; i++ {
		gutter := " "
		if isMatch[i] {
			gutter = Warn.Render("▌")
		}
		rows = append(rows, gutter+p.lines[i])
	}
	return Block(rows, width, height)
}
