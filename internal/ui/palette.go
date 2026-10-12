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
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// maxResults bounds how many matches a palette keeps.
const maxResults = 200

// Item is one entry of a palette.
type Item struct {
	// Label is what is shown and matched against.
	Label string
	// Hint is shown dimmed next to the label, e.g. a key binding.
	Hint string
	// Value identifies the entry to whoever opened the palette.
	Value any
}

// Palette is a list filtered by typing.
type Palette struct {
	Input   Input
	items   []Item
	matches []Item
	Cursor  int
	top     int
}

// NewPalette returns a palette over items, in their given order.
func NewPalette(prompt string, items []Item) *Palette {
	p := &Palette{Input: Input{Prompt: prompt}, items: items}
	p.filter()
	return p
}

// filter keeps the items matching the typed text, best first.
func (p *Palette) filter() {
	query := p.Input.Value()
	p.Cursor, p.top = 0, 0

	if query == "" {
		p.matches = p.items[:min(len(p.items), maxResults)]
		return
	}

	type scored struct {
		item  Item
		score int
	}
	var found []scored
	for _, it := range p.items {
		if s, ok := Fuzzy(query, it.Label); ok {
			found = append(found, scored{it, s})
		}
	}
	slices.SortStableFunc(found, func(a, b scored) int { return b.score - a.score })

	p.matches = p.matches[:0:0]
	for _, f := range found[:min(len(found), maxResults)] {
		p.matches = append(p.matches, f.item)
	}
}

// Key applies a key press. It returns the chosen item when the user picked
// one.
func (p *Palette) Key(k tea.KeyPressMsg) (Item, bool) {
	switch k.String() {
	case "enter":
		if p.Cursor < len(p.matches) {
			return p.matches[p.Cursor], true
		}
	case "up", "ctrl+p":
		p.Cursor = max(p.Cursor-1, 0)
	case "down", "ctrl+n":
		p.Cursor = max(min(p.Cursor+1, len(p.matches)-1), 0)
	default:
		if p.Input.Key(k) {
			p.filter()
		}
	}
	return Item{}, false
}

// View renders the palette as a box of the given size.
func (p *Palette) View(title string, width, height int) []string {
	inner := width - 2
	listHeight := height - 3
	p.top = Window(p.Cursor, p.top, listHeight, len(p.matches))

	lines := []string{p.Input.View()}
	for i := p.top; i < min(p.top+listHeight, len(p.matches)); i++ {
		it := p.matches[i]
		row := " " + it.Label
		if it.Hint != "" {
			gap := inner - len([]rune(row)) - len([]rune(it.Hint)) - 1
			row += strings.Repeat(" ", max(gap, 1)) + Dim.Render(it.Hint)
		}
		lines = append(lines, Selected(row, inner, i == p.Cursor, true))
	}
	if len(p.matches) == 0 {
		lines = append(lines, Dim.Render(" no match"))
	}
	return Box(title, lines, width, height, true)
}

// Fuzzy reports whether every character of query appears in text in order,
// ignoring case, and scores how good the match is: characters that follow
// each other or start a word count for more, and shorter texts win ties.
func Fuzzy(query, text string) (score int, ok bool) {
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(text))
	if len(q) == 0 {
		return 0, true
	}

	qi, last := 0, -2
	for i, r := range t {
		if qi == len(q) {
			break
		}
		if r != q[qi] {
			continue
		}
		score++
		if i == last+1 {
			score += 5
		}
		if i == 0 || !unicode.IsLetter(t[i-1]) && !unicode.IsDigit(t[i-1]) {
			score += 3
		}
		last = i
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	return score*100 - len(t), true
}
