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
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Span is the lines of a file, counted from 1, that a line of a rendered
// document shows. A line that shows none, such as the space between two
// paragraphs, has neither.
type Span struct{ From, To int }

// Document is a Markdown file rendered for reading.
type Document struct {
	// Lines are the text, with a paragraph on one line: the pager wraps it.
	Lines []string
	// Hang is what the rows a line is wrapped to start with, after the
	// first, so that they stay inside their list or quote.
	Hang []string
	// Spans tells which lines of the file each line shows.
	Spans []Span
	// Heads are the lines that hold a heading.
	Heads []int
	// Keep says which lines lose their shape when wrapped: the rows of a
	// table, code and rules. They scroll sideways instead.
	Keep []bool
}

// IsMarkdown reports whether a file is Markdown, by its name.
func IsMarkdown(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// The marks a rendered document is drawn with.
var (
	quoteBar   = Dim.Render("┃") + " "
	codeIndent = "    "
	cellBar    = Dim.Render(" │ ")
)

// ruleWidth is the length of a horizontal rule.
const ruleWidth = 40

// Markdown renders a Markdown file. dark picks the colour scheme of the
// code it quotes.
func Markdown(content string, dark bool) Document {
	r := &markdown{src: []byte(content), dark: dark, starts: []int{0}}
	for i, c := range r.src {
		if c == '\n' {
			r.starts = append(r.starts, i+1)
		}
	}
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	r.blocks(md.Parser().Parse(text.NewReader(r.src)), true)
	return r.doc
}

// markdown renders the blocks of a parsed file, one after the other.
type markdown struct {
	src []byte
	// starts is where each line of the file starts.
	starts []int
	dark   bool
	doc    Document
	// keep says the lines emitted now are not to be wrapped.
	keep bool
	// first is what the next line starts with, and rest the ones after it:
	// the bullet of a list item and the space under it.
	first, rest string
}

// line returns the number of the line of the file an offset is on.
func (r *markdown) line(offset int) int {
	return sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > offset })
}

// span tells which lines of the file a block is on.
func (r *markdown) span(n ast.Node) Span {
	if l := n.Lines(); l != nil && l.Len() > 0 {
		return Span{r.line(l.At(0).Start), r.line(l.At(l.Len() - 1).Start)}
	}
	if p := n.Pos(); p >= 0 {
		return Span{r.line(p), r.line(p)}
	}
	return Span{}
}

func (r *markdown) emit(s string, span Span) {
	r.doc.Lines = append(r.doc.Lines, r.first+s)
	r.doc.Hang = append(r.doc.Hang, r.rest)
	r.doc.Spans = append(r.doc.Spans, span)
	r.doc.Keep = append(r.doc.Keep, r.keep)
	r.first = r.rest
}

// whole renders what fn does on lines that are not to be wrapped.
func (r *markdown) whole(fn func()) {
	r.keep = true
	fn()
	r.keep = false
}

// blank leaves a line between two blocks.
func (r *markdown) blank() {
	r.doc.Lines = append(r.doc.Lines, strings.TrimRight(r.rest, " "))
	r.doc.Hang = append(r.doc.Hang, "")
	r.doc.Spans = append(r.doc.Spans, Span{})
	r.doc.Keep = append(r.doc.Keep, false)
}

// inside renders what fn does with first before its first line and rest
// before the others.
func (r *markdown) inside(first, rest string, fn func()) {
	outerFirst, outerRest, n := r.first, r.rest, len(r.doc.Lines)
	r.first, r.rest = outerFirst+first, outerRest+rest
	fn()
	r.first, r.rest = outerFirst, outerRest
	if len(r.doc.Lines) > n {
		r.first = outerRest
	}
}

// blocks renders the blocks inside n, with a line between them when loose.
func (r *markdown) blocks(n ast.Node, loose bool) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if loose && c != n.FirstChild() {
			r.blank()
		}
		r.block(c)
	}
}

func (r *markdown) block(n ast.Node) {
	plain := lipgloss.NewStyle()
	switch n := n.(type) {
	case *ast.Heading:
		r.doc.Heads = append(r.doc.Heads, len(r.doc.Lines))
		switch style := Bold.Foreground(Accent.GetForeground()); n.Level {
		case 1:
			style = style.Reverse(true)
			r.emit(style.Render(" ")+r.inline(n, style)+style.Render(" "), r.span(n))
		case 2:
			r.emit(r.inline(n, style), r.span(n))
		default:
			r.emit(r.inline(n, Bold), r.span(n))
		}

	case *ast.Paragraph, *ast.TextBlock:
		for l := range strings.SplitSeq(r.inline(n, plain), "\n") {
			r.emit(l, r.span(n))
		}

	case *ast.FencedCodeBlock:
		r.code(n, string(n.Language(r.src)))
	case *ast.CodeBlock:
		r.code(n, "")

	case *ast.Blockquote:
		r.inside(quoteBar, quoteBar, func() { r.blocks(n, true) })

	case *ast.List:
		number := n.Start
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			if !n.IsTight && item != n.FirstChild() {
				r.blank()
			}
			bullet := "• "
			if n.IsOrdered() {
				bullet = fmt.Sprintf("%d. ", number)
				number++
			}
			r.inside(bullet, strings.Repeat(" ", ansi.StringWidth(bullet)), func() {
				lines := len(r.doc.Lines)
				r.blocks(item, !n.IsTight)
				// An item with nothing in it still shows its bullet.
				if len(r.doc.Lines) == lines {
					r.emit("", r.span(item))
				}
			})
		}

	case *ast.ThematicBreak:
		r.whole(func() { r.emit(Dim.Render(strings.Repeat("─", ruleWidth)), r.span(n)) })

	case *ast.HTMLBlock:
		r.whole(func() {
			for i := range n.Lines().Len() {
				r.raw(n.Lines().At(i))
			}
			if n.HasClosure() {
				r.raw(n.ClosureLine)
			}
		})

	case *east.Table:
		r.whole(func() { r.table(n) })

	default:
		r.blocks(n, true)
	}
}

// raw shows a line of the file as it is, such as one of HTML.
func (r *markdown) raw(seg text.Segment) {
	l := strings.TrimRight(string(seg.Value(r.src)), "\r\n")
	n := r.line(seg.Start)
	r.emit(Dim.Render(expandTabs(l)), Span{n, n})
}

// code renders a block of code, coloured when its language is known.
func (r *markdown) code(n ast.Node, language string) {
	var source []string
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		source = append(source, strings.TrimRight(string(seg.Value(r.src)), "\r\n"))
	}
	styled := source
	if language != "" {
		if h := colour(lexers.Get(language), strings.Join(source, "\n"), r.dark); len(h) == len(source) {
			styled = h
		}
	}
	r.inside(codeIndent, codeIndent, func() {
		r.whole(func() {
			for i, l := range styled {
				at := r.line(n.Lines().At(i).Start)
				r.emit(expandTabs(l), Span{at, at})
			}
		})
	})
}

// table renders a table with its columns lined up.
func (r *markdown) table(n *east.Table) {
	var rows [][]string
	var spans []Span
	var widths []int
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		style := lipgloss.NewStyle()
		if _, ok := row.(*east.TableHeader); ok {
			style = Bold
		}
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			s := strings.ReplaceAll(r.inline(cell, style), "\n", " ")
			if len(cells) == len(widths) {
				widths = append(widths, 0)
			}
			widths[len(cells)] = max(widths[len(cells)], ansi.StringWidth(s))
			cells = append(cells, s)
		}
		rows = append(rows, cells)
		spans = append(spans, r.span(row))
	}

	for i, cells := range rows {
		var b strings.Builder
		for j, w := range widths {
			var cell string
			if j < len(cells) {
				cell = cells[j]
			}
			left, right := 0, w-ansi.StringWidth(cell)
			if j < len(n.Alignments) {
				switch n.Alignments[j] {
				case east.AlignRight:
					left, right = right, 0
				case east.AlignCenter:
					left = right / 2
					right -= left
				}
			}
			if j > 0 {
				b.WriteString(cellBar)
			}
			b.WriteString(strings.Repeat(" ", left))
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", right))
		}
		r.emit(strings.TrimRight(b.String(), " "), spans[i])

		// A rule parts the header from the rows.
		if i == 0 {
			rule := make([]string, len(widths))
			for j, w := range widths {
				rule[j] = strings.Repeat("─", w)
			}
			r.emit(Dim.Render(strings.Join(rule, "─┼─")), Span{})
		}
	}
}

// inline renders the text inside n in a style, which what it holds, such
// as emphasis, adds to. A line break that is meant is a newline.
func (r *markdown) inline(n ast.Node, style lipgloss.Style) string {
	var b strings.Builder
	write := func(style lipgloss.Style, s string) {
		if s != "" {
			b.WriteString(style.Render(s))
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			v := c.Value(r.src)
			if !c.IsRaw() {
				v = util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(v)))
			}
			write(style, string(v))
			switch {
			case c.HardLineBreak():
				b.WriteString("\n")
			case c.SoftLineBreak():
				write(style, " ")
			}
		case *ast.String:
			write(style, string(c.Value))
		case *ast.CodeSpan:
			write(style.Foreground(Warn.GetForeground()), strings.ReplaceAll(r.text(c), "\n", " "))
		case *ast.Emphasis:
			if c.Level >= 2 {
				b.WriteString(r.inline(c, style.Bold(true)))
			} else {
				b.WriteString(r.inline(c, style.Italic(true)))
			}
		case *east.Strikethrough:
			b.WriteString(r.inline(c, style.Strikethrough(true)))
		case *ast.Link:
			label := r.inline(c, style.Underline(true).Foreground(Info.GetForeground()))
			b.WriteString(label)
			// Where it leads is shown too, as it cannot be followed.
			if to := string(c.Destination); to != "" && to != ansi.Strip(label) && !strings.HasPrefix(to, "#") {
				b.WriteString(Dim.Render(" (" + to + ")"))
			}
		case *ast.AutoLink:
			write(style.Underline(true).Foreground(Info.GetForeground()), string(c.Label(r.src)))
		case *ast.Image:
			b.WriteString(Dim.Render("[image: " + r.text(c) + "]"))
		case *ast.RawHTML:
			for i := range c.Segments.Len() {
				seg := c.Segments.At(i)
				b.WriteString(Dim.Render(strings.ReplaceAll(string(seg.Value(r.src)), "\n", " ")))
			}
		case *east.TaskCheckBox:
			if c.IsChecked {
				write(style, "☑ ")
			} else {
				write(style, "☐ ")
			}
		default:
			b.WriteString(r.inline(c, style))
		}
	}
	return b.String()
}

// text returns the text inside n without any styling.
func (r *markdown) text(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Value(r.src))
		case *ast.String:
			b.Write(c.Value)
		default:
			b.WriteString(r.text(c))
		}
	}
	return b.String()
}
