package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// maxHighlight is the largest file that gets syntax colours.
const maxHighlight = 512 << 10

// expandTabs replaces tabs, whose width the terminal decides, with spaces.
func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

// IsBinary reports whether data does not look like text.
func IsBinary(data []byte) bool {
	head := data[:min(len(data), 8000)]
	return strings.ContainsRune(string(head), 0) || !utf8.Valid(trimPartialRune(head))
}

// trimPartialRune drops a multi-byte character cut in half at the end.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// FileLines renders the content of a file as numbered lines, with syntax
// colours when the language is known. dark picks the colour scheme.
func FileLines(name, content string, dark bool) []string {
	content = strings.TrimSuffix(content, "\n")
	source := strings.Split(content, "\n")
	styled := source
	if len(content) <= maxHighlight {
		if h := highlight(name, content, dark); len(h) == len(source) {
			styled = h
		}
	}

	width := len(fmt.Sprint(len(source)))
	lines := make([]string, len(source))
	for i, l := range styled {
		lines[i] = Dim.Render(fmt.Sprintf("%*d ", width, i+1)) + expandTabs(l)
	}
	return lines
}

// highlight colours source code line by line. It returns nil when the
// language is unknown or the code cannot be tokenised.
func highlight(name, content string, dark bool) []string {
	lexer := lexers.Match(name)
	if lexer == nil {
		return nil
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, content)
	if err != nil {
		return nil
	}

	style := styles.Get("github")
	if dark {
		style = styles.Get("monokai")
	}
	formatter := formatters.Get("terminal256")

	var lines []string
	for _, tokens := range chroma.SplitTokensIntoLines(it.Tokens()) {
		var b strings.Builder
		if err := formatter.Format(&b, style, chroma.Literator(tokens...)); err != nil {
			return nil
		}
		lines = append(lines, strings.TrimRight(b.String(), "\n"))
	}
	return lines
}

// DiffLines colours a unified diff and returns its lines together with the
// index of each hunk header.
func DiffLines(diff string) (lines []string, hunks []int) {
	for i, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		l = expandTabs(l)
		switch {
		case strings.HasPrefix(l, "@@"):
			hunks = append(hunks, i)
			l = Info.Render(l)
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"),
			strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "),
			strings.HasPrefix(l, "new file"), strings.HasPrefix(l, "deleted file"),
			strings.HasPrefix(l, "rename "), strings.HasPrefix(l, "similarity "):
			l = Bold.Render(l)
		case strings.HasPrefix(l, "+"):
			l = Good.Render(l)
		case strings.HasPrefix(l, "-"):
			l = Bad.Render(l)
		}
		lines = append(lines, l)
	}
	return lines, hunks
}
