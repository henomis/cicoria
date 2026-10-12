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
	tea "charm.land/bubbletea/v2"
)

// Input is a one-line text field.
type Input struct {
	Prompt string
	value  []rune
	// pos is where the cursor is: before the character of that index.
	pos int
}

// Value returns the text typed so far.
func (in *Input) Value() string { return string(in.value) }

// Set replaces the text and puts the cursor at its end.
func (in *Input) Set(s string) {
	in.value = []rune(s)
	in.pos = len(in.value)
}

// isBreak reports whether a character separates words or path components.
func isBreak(r rune) bool { return r == '/' || r == ' ' }

// wordLeft returns where the word or path component before the cursor
// starts, and wordRight where the one after it ends.
func (in *Input) wordLeft() int {
	n := in.pos
	for n > 0 && isBreak(in.value[n-1]) {
		n--
	}
	for n > 0 && !isBreak(in.value[n-1]) {
		n--
	}
	return n
}

func (in *Input) wordRight() int {
	n := in.pos
	for n < len(in.value) && isBreak(in.value[n]) {
		n++
	}
	for n < len(in.value) && !isBreak(in.value[n]) {
		n++
	}
	return n
}

// cut removes the characters from index a up to b and leaves the cursor
// where they were. It reports whether there were any.
func (in *Input) cut(a, b int) bool {
	if a < 0 || a >= b {
		return false
	}
	in.value = append(in.value[:a:a], in.value[b:]...)
	in.pos = a
	return true
}

// Key applies a key press and reports whether it changed the text.
func (in *Input) Key(k tea.KeyPressMsg) bool {
	in.pos = max(min(in.pos, len(in.value)), 0)
	switch k.String() {
	case "left", "ctrl+b":
		in.pos = max(in.pos-1, 0)
		return false
	case "right", "ctrl+f":
		in.pos = min(in.pos+1, len(in.value))
		return false
	case "home", "ctrl+a":
		in.pos = 0
		return false
	case "end", "ctrl+e":
		in.pos = len(in.value)
		return false
	case "ctrl+left", "alt+left", "alt+b":
		in.pos = in.wordLeft()
		return false
	case "ctrl+right", "alt+right", "alt+f":
		in.pos = in.wordRight()
		return false
	case "backspace":
		return in.cut(in.pos-1, in.pos)
	case "delete", "ctrl+d":
		return in.cut(in.pos, min(in.pos+1, len(in.value)))
	case "ctrl+u":
		return in.cut(0, in.pos)
	case "ctrl+k":
		return in.cut(in.pos, len(in.value))
	case "ctrl+w", "alt+backspace":
		// Delete back to the start of the last word or path component.
		return in.cut(in.wordLeft(), in.pos)
	}
	if k.Text != "" && k.Mod&^tea.ModShift == 0 {
		text := []rune(k.Text)
		in.value = append(in.value[:in.pos:in.pos], append(text, in.value[in.pos:]...)...)
		in.pos += len(text)
		return true
	}
	return false
}

// View renders the field with a block cursor where the next character goes.
func (in *Input) View() string {
	pos := max(min(in.pos, len(in.value)), 0)
	under, after := " ", ""
	if pos < len(in.value) {
		under, after = string(in.value[pos]), string(in.value[pos+1:])
	}
	return Accent.Render(in.Prompt) + string(in.value[:pos]) + Reverse.Render(under) + after
}
