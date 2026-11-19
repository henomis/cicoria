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

package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// entry is one line of the timeline under the bars: a session that needs
// you or works now, or something you did.
type entry struct {
	at       time.Time
	id, name string
	text     string
	// outcome is what came of it, as " → done in 12m, 3 files", and said
	// what the agent said at the end of that turn.
	outcome, said string
	// bad marks what went wrong, as a turn that failed.
	bad bool
	// now marks a line about what a session does now, not a thing done.
	now bool
}

// entryKey says which line is under the cursor, wherever new ones push it.
type entryKey struct {
	at       time.Time
	id, text string
}

func (e entry) key() entryKey {
	// A line about now keeps its place while its words change.
	if e.now {
		return entryKey{id: e.id}
	}
	return entryKey{e.at, e.id, e.text}
}

// result is what came of a turn.
type result struct {
	text, said string
	bad        bool
}

// quoted returns the kept text of an event between quotes, if any.
func quoted(e session.Event) string {
	if e.X == "" {
		return ""
	}
	return " “" + e.X + "”"
}

// sentFrom names where a message came from, as the turn shown says it.
func sentFrom(from string) string {
	switch from {
	case fromNotes:
		return "review notes"
	case fromCard:
		return "the card's prompt"
	case fromAsk:
		return "a message from another session"
	case fromAnswer:
		return "the answer of another session"
	}
	return "a message"
}

// sends reports whether an event gives the agent a message: one that can
// start a turn.
func sends(e session.Event) bool {
	return e.K == session.KindSend || e.K == session.KindPromptTyped
}

// startedBy returns the start of the turn the message of event k started,
// or -1 when none is known to have.
func (l lane) startedBy(k int) int {
	e := l.events[k]
	for j := k + 1; j < len(l.events) && l.events[j].T.Sub(e.T) <= fromQueueWithin; j++ {
		switch f := l.events[j]; {
		case f.K == session.KindTurnStart:
			return j
		case sends(f):
			// Another message came first: the turn is its own.
			return -1
		}
	}
	return -1
}

// causeOf returns the event whose message started the turn that starts at
// j, or -1.
func (l lane) causeOf(j int) int {
	for k := j - 1; k >= 0 && l.events[j].T.Sub(l.events[k].T) <= fromQueueWithin; k-- {
		if sends(l.events[k]) {
			return k
		}
	}
	return -1
}

// delivery returns the event that sent the message queued at k, or -1
// while it was not sent.
func (l lane) delivery(k int) int {
	q := l.events[k]
	for j := k + 1; j < len(l.events); j++ {
		f := l.events[j]
		if f.K == session.KindSend && f.Src == "" && f.N == q.N && f.X == q.X {
			return j
		}
		// Taken back from the queue.
		if f.K == session.KindUnqueue && f.N == q.N && f.X == q.X {
			return -1
		}
	}
	return -1
}

// turnResult says how the turn that starts at j went.
func (l lane) turnResult(j int, now time.Time) result {
	began := l.events[j].T
	for _, f := range l.events[j+1:] {
		took := span(f.T.Sub(began))
		switch f.K {
		case session.KindTurnEnd:
			text := " → done in " + took
			if n := len(f.Files); n == 1 {
				text += ", 1 file"
			} else if n > 1 {
				text += fmt.Sprintf(", %d files", n)
			}
			return result{text: text, said: f.X}
		case session.KindTurnFailed:
			return result{text: " → ✕ failed after " + took, bad: true}
		case session.KindInterrupted:
			return result{text: " → you stopped it after " + took}
		case session.KindTurnStart, session.KindSessionClose:
			return result{text: " → ran " + took}
		}
	}
	switch {
	case l.s == nil:
		return result{}
	case l.s.Status == agent.NeedsInput:
		return result{text: fmt.Sprintf(" → worked %s, waits for you since %s", span(l.s.Since.Sub(began)), l.s.Since.Format("15:04"))}
	case l.s.Status == agent.Working:
		return result{text: " → working, " + span(now.Sub(began)) + " so far"}
	}
	return result{}
}

// outcome says what came of the message of event k: how the turn it
// started went, or that it waits in the queue.
func (l lane) outcome(k int, now time.Time) result {
	e := l.events[k]
	if e.K == session.KindQueue {
		d := l.delivery(k)
		if d < 0 {
			if l.s != nil && slices.ContainsFunc(l.s.Queue, func(q session.Message) bool { return len([]rune(q.Text)) == e.N }) {
				return result{text: " → still in the queue"}
			}
			return result{}
		}
		r := l.outcome(d, now)
		// Sent later, it says when.
		if sent := l.events[d].T; sent.Sub(e.T) >= time.Minute {
			rest := strings.TrimPrefix(r.text, " →")
			r.text = " → sent " + sent.Format("15:04")
			if rest != "" {
				r.text += "," + rest
			}
		}
		return r
	}
	if j := l.startedBy(k); j >= 0 {
		return l.turnResult(j, now)
	}
	return result{}
}

// near returns the event of a kind on the lane within a couple of seconds
// of event k, before or after it, or -1.
func (l lane) near(k int, kind session.Kind, ok func(session.Event) bool) int {
	at := l.events[k].T
	for j, f := range l.events {
		if j != k && f.K == kind && f.T.Sub(at).Abs() <= 2*time.Second && (ok == nil || ok(f)) {
			return j
		}
	}
	return -1
}

// say returns the line event k of a lane is, if it is something you did,
// or something that went wrong that no line of yours says.
func (l lane) say(k int, name func(id string) string, now time.Time) (entry, bool) {
	e := l.events[k]
	line := entry{at: e.T, id: l.id, name: l.name}
	web := ""
	if e.Src == session.FromWeb {
		web = " from the web"
	}
	with := func(text string, r result) (entry, bool) {
		line.text, line.outcome, line.said, line.bad = text, r.text, r.said, r.bad
		return line, true
	}
	switch e.K {
	case session.KindAnswer:
		text := "answered"
		if e.V != "" {
			text += " “" + e.V + "”"
		}
		text += web
		// How long the agent waited for it.
		for j := k - 1; j >= 0; j-- {
			if p := l.events[j]; p.K == session.KindStatus {
				if p.V == "needs input" && e.T.Sub(p.T) >= time.Minute {
					text += " · it waited " + span(e.T.Sub(p.T))
				}
				break
			}
		}
		return with(text, result{})
	case session.KindPromptTyped:
		text := "typed a prompt"
		if e.X != "" {
			text = "typed" + quoted(e)
		} else if e.N > 0 {
			text += " (its text is not kept)"
		}
		return with(text, l.outcome(k, now))
	case session.KindQueue, session.KindSend:
		// What the queue sends was said when it was queued.
		if e.K == session.KindSend && e.Src == "" {
			return entry{}, false
		}
		verb := "queued"
		if e.K == session.KindSend {
			verb = "sent"
		}
		var text string
		switch e.V {
		case fromCard:
			// The card's line says it.
			return entry{}, false
		case fromNotes:
			text = verb + " review notes" + web
		case fromAsk:
			if e.To != "" {
				text = "asked" + quoted(e) + " from " + name(e.To)
			} else {
				text = "got" + quoted(e) + " from another session"
			}
		case fromAnswer:
			text = "got the answer it asked another session for"
		default:
			what := quoted(e)
			if what == "" {
				what = " a message"
			}
			if e.To != "" {
				what += ", for " + name(e.To) + " to answer"
			}
			text = verb + what + web
		}
		return with(text, l.outcome(k, now))
	case session.KindInterrupted:
		return with("stopped the turn"+web, result{})
	case session.KindUnqueue:
		return with("took back"+quoted(e)+web, result{})
	case session.KindWorktreeAdd:
		// A card started in a worktree says it.
		if l.near(k, session.KindCardStart, nil) >= 0 {
			return entry{}, false
		}
		return with("made the worktree "+tilde(e.V), result{})
	case session.KindCardStart:
		text := "started the card “" + e.V + "”"
		if w := l.near(k, session.KindWorktreeAdd, nil); w >= 0 {
			text += " in " + tilde(l.events[w].V)
		}
		var r result
		if q := l.near(k, session.KindQueue, func(f session.Event) bool { return f.V == fromCard }); q >= 0 {
			r = l.outcome(q, now)
		}
		return with(text, r)
	case session.KindTurnFailed:
		// A turn a message of yours started fails on that message's line.
		for j := k - 1; j >= 0; j-- {
			if l.events[j].K == session.KindTurnStart {
				if l.causeOf(j) >= 0 {
					return entry{}, false
				}
				line.bad = true
				return with("✕ a turn failed after "+span(e.T.Sub(l.events[j].T)), result{bad: true})
			}
		}
		line.bad = true
		return with("✕ a turn failed", result{bad: true})
	case session.KindSessionClose:
		return with("closed it", result{})
	}
	return entry{}, false
}

// question returns what a session that needs you asks, as far as it is
// known: the agent's words, the tool it asks for, or the question on its
// screen.
func question(s *session.Session) string {
	var parts []string
	if q := strings.TrimSpace(s.Question); q != "" {
		parts = append(parts, q)
	}
	// The tool, unless the agent's words name it already.
	switch tool := strings.TrimSpace(s.Tool.Name + " " + s.Tool.Detail); {
	case s.Tool.Name == "":
	case len(parts) > 0 && strings.Contains(parts[0], s.Tool.Name):
		if d := strings.TrimSpace(s.Tool.Detail); d != "" {
			parts = append(parts, d)
		}
	default:
		parts = append(parts, tool)
	}
	if len(parts) == 0 && s.Term != nil {
		if menu, ok := agent.ReadMenu(s.Term.String()); ok && menu.Prompt != "" {
			parts = append(parts, menu.Prompt)
		}
	}
	_, text := session.Clip(strings.Join(parts, ": "), session.TextPreview)
	return text
}

// nowLines returns the lines about the sessions that need you, the one that
// has waited longest first, then those at work.
func nowLines(lanes []lane, now time.Time) []entry {
	var waiting, working []entry
	for _, l := range lanes {
		s := l.s
		if s == nil {
			continue
		}
		line := entry{at: now, id: l.id, name: l.name, now: true}
		switch s.Status {
		case agent.NeedsInput:
			line.text = "▲ waits for you " + span(now.Sub(s.Since))
			if q := question(s); q != "" {
				line.text += ": “" + q + "”"
			}
			waiting = append(waiting, line)
		case agent.Working:
			line.text = "● working " + span(now.Sub(s.Since))
			// On what: the message of the turn under way.
			for j := len(l.events) - 1; j >= 0; j-- {
				if l.events[j].K == session.KindTurnStart {
					if c := l.causeOf(j); c >= 0 && l.events[c].X != "" {
						line.text += " on " + strings.TrimSpace(quoted(l.events[c]))
					}
					break
				}
			}
			working = append(working, line)
		}
	}
	slices.SortStableFunc(waiting, func(a, b entry) int { return compareSince(lanes, a.id, b.id) })
	slices.SortStableFunc(working, func(a, b entry) int { return compareSince(lanes, a.id, b.id) })
	return append(waiting, working...)
}

// compareSince orders two sessions by how long they have been in their
// status, the longest first.
func compareSince(lanes []lane, a, b string) int {
	since := func(id string) time.Time {
		for _, l := range lanes {
			if l.id == id && l.s != nil {
				return l.s.Since
			}
		}
		return time.Time{}
	}
	return since(a).Compare(since(b))
}

// busyIn reports whether a lane did anything in the time of a timeline.
func busyIn(t ui.Timeline, l lane) bool {
	return slices.ContainsFunc(t.States(l.periods), func(st ui.State) bool { return st > ui.StateIdle })
}
