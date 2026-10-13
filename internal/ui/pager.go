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

package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// gutter is the width of the columns at the left of the text.
const gutter = 2

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
	// Wrap says a line too long for the screen goes on in the rows under it
	// instead of being cut.
	Wrap bool
	// Hang is what the rows a line is wrapped to start with, after the
	// first: the indent of a list item. It may be nil, and SetContent
	// drops it.
	Hang []string
	// width is how wide the pager was last drawn, and wrapped the rows of
	// the lines that were wrapped to it so far.
	width   int
	wrapped map[int][]string
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
	p.Top, p.Cursor, p.selecting, p.wrapped, p.Hang = 0, 0, false, nil, nil
	p.Search(p.query)
}

// Len returns the number of lines.
func (p *Pager) Len() int { return len(p.lines) }

// rows returns the rows line i is shown on: one, unless long lines are
// wrapped. A line that would not fit on the screen is cut at height rows.
func (p *Pager) rows(i, height int) []string {
	text := p.width - gutter
	if !p.Wrap || text < 1 {
		return p.lines[i : i+1]
	}
	rows, ok := p.wrapped[i]
	if !ok {
		rows = p.wrap(i, text)
		if p.wrapped == nil {
			p.wrapped = map[int][]string{}
		}
		p.wrapped[i] = rows
	}
	if height = max(height, 1); len(rows) > height {
		rows = append(slices.Clone(rows[:height-1]), ansi.Truncate(rows[height-1], text-1, "")+"…")
	}
	return rows
}

// minHang is the least width of text that is still indented under a line.
const minHang = 20

// wrap returns line i on rows of width cells.
func (p *Pager) wrap(i, width int) []string {
	var hang string
	if i < len(p.Hang) {
		hang = p.Hang[i]
	}
	indent := ansi.StringWidth(hang)
	if indent == 0 || width-indent < minHang {
		return strings.Split(lipgloss.Wrap(p.lines[i], width, ""), "\n")
	}
	// The line starts with as much as its other rows are indented by.
	l := p.lines[i]
	rows := strings.Split(lipgloss.Wrap(ansi.Cut(l, indent, ansi.StringWidth(l)), width-indent, ""), "\n")
	for j := range rows {
		if j == 0 {
			rows[j] = ansi.Cut(l, 0, indent) + rows[j]
		} else {
			rows[j] = hang + rows[j]
		}
	}
	return rows
}

// topFor returns the first line that can be at the top of the screen with
// line last still whole on it.
func (p *Pager) topFor(last, height int) int {
	top, used := last, len(p.rows(last, height))
	for top > 0 && used+len(p.rows(top-1, height)) <= height {
		top--
		used += len(p.rows(top, height))
	}
	return top
}

// bottom returns the last line that is whole on the screen.
func (p *Pager) bottom(height int) int {
	last, used := p.Top, len(p.rows(p.Top, height))
	for last+1 < len(p.lines) && used+len(p.rows(last+1, height)) <= height {
		last++
		used += len(p.rows(last, height))
	}
	return last
}

// LineAt returns the line shown on a row of the screen, counted from 0.
func (p *Pager) LineAt(row, height int) int {
	p.clamp(height)
	line := p.Top
	for used := 0; line+1 < len(p.lines); line++ {
		if used += len(p.rows(line, height)); row < used {
			break
		}
	}
	return line
}

// clamp keeps the scroll position within the text for a body of height rows,
// and the cursor on a line that is shown.
func (p *Pager) clamp(height int) {
	if len(p.lines) == 0 {
		p.Top, p.Cursor = 0, 0
		return
	}
	p.Top = max(min(p.Top, p.topFor(len(p.lines)-1, height)), 0)
	p.Cursor = max(min(p.Cursor, p.bottom(height)), p.Top)
}

// Scroll moves the text by delta rows, a whole line at a time and by one
// line at least. The cursor follows when it would be left behind.
func (p *Pager) Scroll(delta, height int) {
	p.clamp(height)
	down := delta > 0
	for n := 0; down && delta > 0 && p.Top < len(p.lines)-1; n++ {
		tall := len(p.rows(p.Top, height))
		if n > 0 && tall > delta {
			break
		}
		delta -= tall
		p.Top++
	}
	for n := 0; !down && delta < 0 && p.Top > 0; n++ {
		tall := len(p.rows(p.Top-1, height))
		if n > 0 && tall > -delta {
			break
		}
		delta += tall
		p.Top--
	}
	p.clamp(height)
}

// Move moves the cursor by delta lines, and the text with it when it would
// leave the screen.
func (p *Pager) Move(delta, height int) {
	p.SetCursor(p.Cursor+delta, height)
}

// SetCursor puts the cursor on a line.
func (p *Pager) SetCursor(line, height int) {
	p.clamp(height)
	line = max(min(line, len(p.lines)-1), 0)
	switch {
	case line < p.Top:
		p.Top = line
	case len(p.lines) > 0 && line > p.bottom(height):
		p.Top = p.topFor(line, height)
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
		s = fmt.Sprintf("%d-%d/%d", p.Top+1, p.bottom(height)+1, n)
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
	if width != p.width {
		p.width, p.wrapped = width, nil
	}
	p.clamp(height)

	isMatch := map[int]bool{}
	for _, m := range p.matches {
		isMatch[m] = true
	}

	// The gutter has a column for the lines that carry a note, and one for
	// the cursor, the selection and the search matches.
	from, to := p.Selection()
	rows := make([]string, 0, height)
	for i := p.Top; i < len(p.lines) && len(rows) < height; i++ {
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
		// Only the first row of a wrapped line says it carries a note.
		for j, row := range p.rows(i, height) {
			if j > 0 {
				note = " "
			}
			rows = append(rows, note+bar+row)
		}
	}
	return Block(rows, width, height)
}
