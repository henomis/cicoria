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
	"regexp"
	"strconv"
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
	return colour(lexers.Match(name), content, dark)
}

// colour colours source code in the language of a lexer, which may be nil.
func colour(lexer chroma.Lexer, content string, dark bool) []string {
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

// LineRef says which line of the file a line of a diff shows: its number in
// the old file, in the new one, or both. A line that shows none, such as a
// header, has neither.
type LineRef struct{ Old, New int }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// DiffRefs tells, for every line DiffLines returns, which line of the file
// it shows.
func DiffRefs(diff string) []LineRef {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	refs := make([]LineRef, len(lines))
	// oldLeft and newLeft are the lines the hunk still has to show.
	var old, cur, oldLeft, newLeft int
	for i, l := range lines {
		if oldLeft <= 0 && newLeft <= 0 {
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				old, oldLeft = hunkRange(m[1], m[2])
				cur, newLeft = hunkRange(m[3], m[4])
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "+"):
			refs[i] = LineRef{New: cur}
			cur, newLeft = cur+1, newLeft-1
		case strings.HasPrefix(l, "-"):
			refs[i] = LineRef{Old: old}
			old, oldLeft = old+1, oldLeft-1
		case strings.HasPrefix(l, "\\"):
			// "No newline at end of file" is about the line before.
		default:
			refs[i] = LineRef{Old: old, New: cur}
			old, oldLeft, cur, newLeft = old+1, oldLeft-1, cur+1, newLeft-1
		}
	}
	return refs
}

// hunkRange reads the first line and the length of one side of a hunk
// header; a length that is left out is one.
func hunkRange(start, length string) (int, int) {
	n, _ := strconv.Atoi(start)
	count := 1
	if length != "" {
		count, _ = strconv.Atoi(length)
	}
	return n, count
}
