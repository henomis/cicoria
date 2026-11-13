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

package session

import (
	"strings"

	"github.com/henomis/cicoria/internal/agent"
)

// Card is a piece of work written down in the backlog, for a session that
// is not started yet. It becomes one only when the user starts it: then
// Prompt is the first message its agent gets.
type Card struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	// Dir is the directory the card was written for, offered as the one its
	// session works in.
	Dir string `json:"dir,omitempty"`
}

// cardCut is the line from which the text of a card is ignored, as git
// ignores what follows the scissors in a commit message.
const cardCut = "# ------------------------ >8 ------------------------"

// cardHelp follows cardCut in the text of a card, to say how it is read.
const cardHelp = `# The first line is the title of the card, and what follows is the
# first message its agent gets; a card of one line is its own message.
# Everything from the line above down is ignored. Leave the card empty
# to keep it as it was, or not to add it.
`

// CardText is the text of a card as it is written in an editor.
func CardText(c Card) string {
	var b strings.Builder
	if c.Title != "" {
		b.WriteString(c.Title + "\n")
		if c.Prompt != "" && c.Prompt != c.Title {
			b.WriteString("\n" + c.Prompt + "\n")
		}
	}
	b.WriteString("\n" + cardCut + "\n" + cardHelp)
	return b.String()
}

// ParseCard reads the text of a card written in an editor. ok is false when
// it holds nothing.
func ParseCard(text string) (title, prompt string, ok bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if i := strings.Index(text, "\n"+cardCut); i >= 0 {
		text = text[:i]
	} else if strings.HasPrefix(text, cardCut) {
		text = ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", false
	}
	title, rest, _ := strings.Cut(text, "\n")
	title, prompt = strings.TrimSpace(title), strings.TrimSpace(rest)
	if prompt == "" {
		prompt = title
	}
	return title, prompt, true
}

// Lane is the column of the board a session is in: what it asks of the
// user. It follows from the session's state alone, so it is never set.
type Lane int

const (
	// LaneNeedsYou holds the sessions waiting for an answer, and those whose
	// agent could not start.
	LaneNeedsYou Lane = iota
	// LaneWorking holds the sessions whose agent is at work.
	LaneWorking
	// LaneReview holds the idle sessions with something to look at: a turn
	// that ended while the user was elsewhere, changes, or notes not sent.
	LaneReview
	// LaneSettled holds the rest: idle with nothing new, or exited.
	LaneSettled
)

// Lanes is how many lanes there are.
const Lanes = 4

// Lane says which column of the board the session is in. changed says its
// work tree has changes, or commits since the session started.
func (s *Session) Lane(changed bool) Lane {
	switch {
	case s.Err != nil && s.Term == nil:
		return LaneNeedsYou
	case s.Status == agent.NeedsInput:
		return LaneNeedsYou
	case s.Status == agent.Working || s.Status == agent.Starting:
		return LaneWorking
	case s.Status == agent.Idle && (s.Unseen || changed || len(s.Notes) > 0):
		return LaneReview
	}
	return LaneSettled
}
