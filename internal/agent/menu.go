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

package agent

import (
	"regexp"
	"strconv"
	"strings"
)

// Menu is a question an agent draws on its screen with numbered choices,
// one of them under its cursor, as Claude Code, Codex CLI and Copilot CLI
// ask for permission.
type Menu struct {
	// Prompt is the line above the choices, when there is one: the
	// question in the agent's words.
	Prompt string
	// Choices are the labels of the choices, in order.
	Choices []string
	// Selected is the choice under the cursor, or -1 when no cursor is
	// drawn.
	Selected int
}

// menuLine is a numbered choice: an optional cursor, its number and its
// label.
var menuLine = regexp.MustCompile(`^\s*([❯›>▶→●]\s*)?(\d{1,2})[.)]\s+(\S.*)$`)

// menuShortcut is the key a choice names at its end, such as "(esc)":
// the page answers with the choice, not the key.
var menuShortcut = regexp.MustCompile(`(?i)\s*\((?:y|n|a|p|esc|enter|tab|shift\+tab)\)$`)

// frame is what draws a box around a menu, and is not part of its words.
const frame = "│┃|╭╮╰╯─━┌┐└┘"

// menuReach is how many lines of text may follow the last choice: the hints
// under a menu, but not a whole answer the agent wrote after a list.
const menuReach = 4

// ReadMenu finds the menu at the bottom of a screen. It takes the last run
// of choices numbered from 1, near enough to the end of the screen; a list
// higher up is something the agent wrote, not a question.
func ReadMenu(screen string) (Menu, bool) {
	var lines []string
	for l := range strings.SplitSeq(screen, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), frame))
		if l != "" {
			lines = append(lines, l)
		}
	}
	// From the bottom up: the last choice, then the ones before it down
	// to the first.
	for end := len(lines) - 1; end >= 0 && end >= len(lines)-1-menuReach; end-- {
		m, ok := menuEndingAt(lines, end)
		if ok {
			return m, true
		}
	}
	return Menu{}, false
}

// menuEndingAt reads a menu whose last choice is lines[end].
func menuEndingAt(lines []string, end int) (Menu, bool) {
	match := menuLine.FindStringSubmatch(lines[end])
	if match == nil {
		return Menu{}, false
	}
	n, _ := strconv.Atoi(match[2])
	if n < 2 {
		return Menu{}, false
	}
	m := Menu{Choices: make([]string, n), Selected: -1}
	want, i := n, end
	// A label that wraps puts a line between two choices: one is let by.
	for skipped := 0; want >= 1 && i >= 0; i-- {
		match := menuLine.FindStringSubmatch(lines[i])
		if match == nil {
			if skipped++; skipped > 1 {
				return Menu{}, false
			}
			continue
		}
		if k, _ := strconv.Atoi(match[2]); k != want {
			return Menu{}, false
		}
		skipped = 0
		m.Choices[want-1] = strings.TrimSpace(menuShortcut.ReplaceAllString(match[3], ""))
		if match[1] != "" {
			m.Selected = want - 1
		}
		want--
	}
	if want != 0 {
		return Menu{}, false
	}
	// i is now the line above the first choice.
	if i >= 0 && menuLine.FindStringSubmatch(lines[i]) == nil {
		m.Prompt = lines[i]
	}
	return m, true
}
