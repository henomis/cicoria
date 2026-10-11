package ui

import (
	tea "charm.land/bubbletea/v2"
)

// Input is a one-line text field.
type Input struct {
	Prompt string
	value  []rune
}

// Value returns the text typed so far.
func (in *Input) Value() string { return string(in.value) }

// Set replaces the text.
func (in *Input) Set(s string) { in.value = []rune(s) }

// Key applies a key press and reports whether it changed the text.
func (in *Input) Key(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace":
		if len(in.value) == 0 {
			return false
		}
		in.value = in.value[:len(in.value)-1]
		return true
	case "ctrl+u":
		in.value = nil
		return true
	}
	if k.Text != "" && k.Mod&^tea.ModShift == 0 {
		in.value = append(in.value, []rune(k.Text)...)
		return true
	}
	return false
}

// View renders the field with a block cursor at the end.
func (in *Input) View() string {
	return Accent.Render(in.Prompt) + string(in.value) + Reverse.Render(" ")
}
