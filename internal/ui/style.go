// Package ui holds the widgets the workspace is drawn with. They keep their
// own state and render to strings; none of them knows about agents.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Colours, from the terminal's own 16-colour palette so that they follow the
// user's theme.
var (
	Accent  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	Dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	Bold    = lipgloss.NewStyle().Bold(true)
	Good    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Bad     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	Warn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	Info    = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	Reverse = lipgloss.NewStyle().Reverse(true)
)

// Fit makes a single line exactly width cells wide, cutting it with an
// ellipsis or padding it with spaces.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := ansi.StringWidth(s); w > width {
		s = ansi.Truncate(s, width, "…")
	}
	if w := ansi.StringWidth(s); w < width {
		s += strings.Repeat(" ", width-w)
	}
	return s
}

// Block makes text exactly width by height cells, cutting or padding it.
func Block(lines []string, width, height int) []string {
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = Fit(lines[i], width)
		} else {
			out[i] = strings.Repeat(" ", max(width, 0))
		}
	}
	return out
}

// Box draws lines inside a titled border, width by height cells overall.
func Box(title string, lines []string, width, height int, focused bool) []string {
	if width < 2 || height < 2 {
		return Block(nil, width, height)
	}

	border := Dim
	if focused {
		border = Accent
	}

	top := "╭"
	if title != "" && width > 6 {
		title = ansi.Truncate(title, width-6, "…")
		top += "─ " + title + " "
	}
	fill := width - 1 - ansi.StringWidth(top)
	top = border.Render(top + strings.Repeat("─", max(fill, 0)) + "╮")

	out := make([]string, 0, height)
	out = append(out, top)
	side := border.Render("│")
	for _, l := range Block(lines, width-2, height-2) {
		out = append(out, side+l+side)
	}
	return append(out, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

// JoinColumns puts blocks of equal height side by side.
func JoinColumns(cols ...[]string) []string {
	var out []string
	for i, col := range cols {
		if i == 0 {
			out = append(out, col...)
			continue
		}
		for j := range out {
			if j < len(col) {
				out[j] += col[j]
			}
		}
	}
	return out
}

// Window returns the first row to show so that cursor stays visible in a
// list of height rows currently scrolled to top.
func Window(cursor, top, height, total int) int {
	if cursor < top {
		top = cursor
	}
	if cursor >= top+height {
		top = cursor - height + 1
	}
	return max(min(top, total-height), 0)
}

// Selected renders a list row, highlighted when it is under the cursor of a
// focused list.
func Selected(row string, width int, selected, focused bool) string {
	row = Fit(row, width)
	switch {
	case selected && focused:
		return Reverse.Render(ansi.Strip(row))
	case selected:
		return Bold.Render(row)
	}
	return row
}

// Overlay draws top over base with its upper left corner at column x, row y.
func Overlay(base, top []string, x, y int) []string {
	out := append([]string{}, base...)
	for i, row := range top {
		if y+i < 0 || y+i >= len(out) {
			continue
		}
		line := out[y+i]
		w := ansi.StringWidth(row)
		out[y+i] = ansi.Cut(line, 0, x) + "\x1b[m" + row + "\x1b[m" + ansi.Cut(line, x+w, ansi.StringWidth(line))
	}
	return out
}
