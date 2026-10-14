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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// maxFile is the most of a file that the viewer loads.
const maxFile = 2 << 20

// fullView is the full-screen view of one file or of its diff.
type fullView struct {
	pager ui.Pager
	// dir is the project the file belongs to, and path its place in it.
	dir  string
	path string
	diff bool
	// rendered says the file is shown as the document its Markdown
	// describes, and spans which lines of the file each line of it shows.
	// raw says the user asked for the text of such files instead.
	rendered bool
	spans    []ui.Span
	raw      bool
	// wrap says long lines of a file or diff are wrapped.
	wrap bool
	// plain holds the text of each line shown, without styling, and refs
	// which line of the file it is. Lines that show none cannot carry a note.
	plain []string
	refs  []ui.LineRef
	// search is set while the user types a search.
	search *ui.Input
}

// viewHeight is the number of text rows of the full-screen view.
func (m *Model) viewHeight() int { return max(m.height-2, 1) }

// openDiff shows the diff of a changed file on the whole screen.
func (m *Model) openDiff(c git.Change) {
	m.view.dir, m.view.path, m.view.diff = m.project(), c.Path, true
	m.view.plain, m.view.refs = nil, nil
	m.view.rendered, m.view.spans = false, nil
	m.view.pager.Title, m.view.pager.Wrap = c.Path, m.view.wrap
	m.mode = modeView

	diff, err := git.Diff(m.view.dir, c, m.changesBase)
	switch {
	case err != nil:
		m.view.pager.SetContent([]string{ui.Bad.Render(err.Error())}, nil)
	case diff == "":
		m.view.pager.SetContent([]string{ui.Dim.Render("no textual differences")}, nil)
	default:
		m.view.pager.SetContent(ui.DiffLines(diff))
		m.view.plain = strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
		m.view.refs = ui.DiffRefs(diff)
	}
	m.markNotes()
}

// showFile shows a file the way it reads best: a Markdown one rendered,
// unless the user asked for its text.
func (m *Model) showFile(path string) {
	if ui.IsMarkdown(path) && !m.view.raw {
		m.openRendered(path)
	} else {
		m.openFile(path)
	}
}

// openRendered shows a Markdown file as the document it describes. One
// that cannot be read whole is shown as text.
func (m *Model) openRendered(path string) {
	m.view.dir = m.project()
	text, size, problem := m.fileText(path)
	if problem != "" || size > maxFile {
		m.openFile(path)
		return
	}
	m.view.path, m.view.diff, m.view.rendered = path, false, true
	m.view.plain, m.view.refs = nil, nil
	m.mode = modeView

	doc := ui.Markdown(text, m.dark)
	m.view.spans = doc.Spans
	m.view.pager.SetContent(doc.Lines, doc.Heads)
	// A paragraph is one line, which only reads wrapped.
	m.view.pager.Title, m.view.pager.Wrap, m.view.pager.Hang = path, true, doc.Hang
	m.markNotes()
}

// openFile shows the text of a file on the whole screen.
func (m *Model) openFile(path string) {
	m.view.dir, m.view.path, m.view.diff = m.project(), path, false
	m.view.rendered, m.view.spans = false, nil
	m.view.pager.Title, m.view.pager.Wrap = path, m.view.wrap
	m.mode = modeView

	lines, source := m.fileLines(path)
	m.view.pager.SetContent(lines, nil)
	m.view.plain, m.view.refs = source, make([]ui.LineRef, len(source))
	for i := range source {
		m.view.refs[i] = ui.LineRef{Old: i + 1, New: i + 1}
	}
	m.markNotes()
}

// fileLines returns a file as the lines to show and, when it is text, the
// lines of the file they show.
func (m *Model) fileLines(path string) (lines, source []string) {
	text, size, problem := m.fileText(path)
	if problem != "" {
		return []string{problem}, nil
	}
	lines = ui.FileLines(path, text, m.dark)
	source = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(source) != len(lines) {
		source = nil
	}
	if size > maxFile {
		lines = append(lines, ui.Warn.Render(fmt.Sprintf("… only the first %d MB are shown", maxFile>>20)))
	}
	return lines, source
}

// fileText returns the start of a file, which is all of a file no larger
// than maxFile, and its size. A file that is not text has a problem, which
// is the line to show in its place.
func (m *Model) fileText(path string) (text string, size int64, problem string) {
	full := filepath.Join(m.view.dir, path)
	st, err := os.Stat(full)
	if err != nil {
		return "", 0, ui.Bad.Render(err.Error())
	}
	if st.IsDir() {
		return "", 0, ui.Dim.Render("a directory")
	}

	f, err := os.Open(full)
	if err != nil {
		return "", 0, ui.Bad.Render(err.Error())
	}
	defer func() { _ = f.Close() }()
	data := make([]byte, min(st.Size(), maxFile))
	n, _ := f.Read(data)
	data = data[:n]

	if ui.IsBinary(data) {
		return "", 0, ui.Dim.Render(fmt.Sprintf("binary file, %d bytes: o opens it outside cicoria", st.Size()))
	}
	return string(data), st.Size(), ""
}

// maxQuote is the most lines a note quotes of what it is about.
const maxQuote = 20

// ref tells which line of the file line i of the view shows, if any.
func (m *Model) ref(i int) (ui.LineRef, bool) {
	if i < 0 || i >= len(m.view.refs) || m.view.refs[i] == (ui.LineRef{}) {
		return ui.LineRef{}, false
	}
	return m.view.refs[i], true
}

// noteAt returns which note of the session on screen is about line i of
// the view, or -1.
func (m *Model) noteAt(i int) int {
	s := m.current()
	r, ok := m.ref(i)
	if s == nil || !ok {
		return -1
	}
	for j, n := range s.Notes {
		if n.Covers(m.view.path, r.Old, r.New) {
			return j
		}
	}
	return -1
}

// markNotes puts a mark in the gutter of the lines that carry a note.
func (m *Model) markNotes() {
	noted := map[int]bool{}
	for i := range m.view.refs {
		if m.noteAt(i) >= 0 {
			noted[i] = true
		}
	}
	// A rendered document shows where the notes on its text are.
	if s := m.current(); s != nil {
		for i, span := range m.view.spans {
			for _, n := range s.Notes {
				if n.Path == m.view.path && !n.Removed && n.From <= span.To && n.To >= span.From {
					noted[i] = true
				}
			}
		}
	}
	m.view.pager.Noted = noted
}

// selected describes the selected lines as a note without its text. It
// reports false when they show no line of the file.
func (m *Model) selected() (session.Note, bool) {
	from, to := m.view.pager.Selection()
	note := session.Note{Path: m.view.path, Removed: true}
	var olds, news []int
	for i := from; i <= to; i++ {
		r, ok := m.ref(i)
		if !ok {
			continue
		}
		if r.New > 0 {
			news = append(news, r.New)
		} else {
			olds = append(olds, r.Old)
		}
		if len(note.Quote) < maxQuote {
			note.Quote = append(note.Quote, m.view.plain[i])
		} else if len(note.Quote) == maxQuote {
			note.Quote = append(note.Quote, "…")
		}
	}
	// Lines of the file as it is now are what the agent can look up; only
	// a selection of nothing but removed lines is told by the old numbers.
	lines := olds
	if len(news) > 0 {
		lines, note.Removed = news, false
	}
	if len(lines) == 0 {
		return note, false
	}
	note.From, note.To = slices.Min(lines), slices.Max(lines)
	return note, true
}

// notesOnText is what a rendered document answers to a note.
const notesOnText = "notes are written on the text of the file: m shows it"

// askNote asks for a note on the selected lines, or for a new text for the
// note the cursor is on.
func (m *Model) askNote() {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return
	}
	if m.view.rendered {
		m.say(notesOnText)
		return
	}
	note, ok := m.selected()
	if !ok {
		m.say("there is no line of the file here to write a note on")
		return
	}
	id, old := s.ID, -1
	if !m.view.pager.Selecting() {
		old = m.noteAt(m.view.pager.Cursor)
	}
	p := &prompt{input: ui.Input{Prompt: "note on " + note.Where() + ": "}}
	if old >= 0 {
		note = s.Notes[old]
		p.input.Prompt = "note on " + note.Where() + ": "
		p.input.Set(note.Text)
	}
	before := note
	p.done = func(text string) tea.Cmd {
		s, _ := m.byID(id)
		if s == nil || text == "" {
			return nil
		}
		note.Text = text
		// The notes may have changed while this one was being written.
		if i := slices.IndexFunc(s.Notes, func(n session.Note) bool { return reflect.DeepEqual(n, before) }); old >= 0 && i >= 0 {
			s.Notes[i] = note
		} else {
			s.Notes = append(s.Notes, note)
		}
		m.view.pager.Unselect()
		m.markNotes()
		m.save()
		return nil
	}
	m.prompt = p
}

// dropNote deletes the note the cursor is on.
func (m *Model) dropNote() {
	if m.view.rendered {
		m.say(notesOnText)
		return
	}
	s := m.current()
	i := m.noteAt(m.view.pager.Cursor)
	if s == nil || i < 0 {
		m.say("no note on this line")
		return
	}
	s.Notes = slices.Delete(s.Notes, i, i+1)
	m.markNotes()
	m.save()
}

// askSendNotes asks how the notes of the session on screen should reach
// its agent.
func (m *Model) askSendNotes() tea.Cmd {
	s := m.current()
	if s == nil || len(s.Notes) == 0 {
		m.say("no notes to send: c writes one on a line of a file or diff")
		return nil
	}
	m.openPalette(fmt.Sprintf("send %d note(s) to %s › ", len(s.Notes), s.Name), []ui.Item{
		{Label: "Send to the agent now", Value: sendNotes{queue: false}},
		{Label: "Add to the queue", Hint: "goes when the agent ends its turn", Value: sendNotes{queue: true}},
	})
	return nil
}

// sendNotes is the choice of how notes are sent: through the queue or not.
type sendNotes struct{ queue bool }

// sendReview hands the notes of the session on screen to its agent as one
// message and forgets them.
func (m *Model) sendReview(queue bool) tea.Cmd {
	s := m.current()
	if s == nil || len(s.Notes) == 0 {
		return nil
	}
	text := session.Review(s.Notes)
	// A question on the agent's screen would take the message for its
	// answer.
	if !queue && s.Status == agent.NeedsInput {
		queue = true
		m.say(s.Name + " is waiting for an answer: the notes are in the queue")
	}
	if !queue && (s.Term == nil || s.Status == agent.Exited) {
		m.say(s.Name + " is not running")
		return nil
	}
	s.Notes = nil
	m.markNotes()
	// The review is over: back to the agent it went to.
	m.focus = focusAgent
	if queue {
		return m.enqueue(s, session.Message{Text: text})
	}
	m.save()
	return m.deliver(s, text)
}

// reloadView reads again what the full-screen view shows, keeping its place.
func (m *Model) reloadView() {
	top, cursor, wrap := m.view.pager.Top, m.view.pager.Cursor, m.view.pager.Wrap
	if c, ok := m.change(m.view.path); m.view.diff && ok {
		m.openDiff(c)
	} else if m.view.rendered {
		m.openRendered(m.view.path)
	} else {
		m.openFile(m.view.path)
	}
	m.view.pager.Top, m.view.pager.Wrap = top, wrap
	m.view.pager.SetCursor(cursor, m.viewHeight())
}

// toggleView switches between a file and its diff.
func (m *Model) toggleView() {
	if m.view.diff {
		m.openFile(m.view.path)
		return
	}
	if c, ok := m.change(m.view.path); ok {
		m.openDiff(c)
	} else {
		m.say(m.view.path + " has no changes")
	}
}

// toggleRendered switches between a Markdown file as a document and its
// text, staying on the same part of it.
func (m *Model) toggleRendered() {
	if !ui.IsMarkdown(m.view.path) {
		m.say(m.view.path + " is not a Markdown file")
		return
	}
	line := m.fileLine()
	m.view.raw = m.view.rendered
	if m.view.rendered {
		m.openFile(m.view.path)
	} else {
		m.openRendered(m.view.path)
	}

	at := line - 1
	if m.view.rendered {
		at = 0
		for i, span := range m.view.spans {
			if span.From > 0 && span.From <= line {
				at = i
			}
		}
	}
	if line > 0 {
		m.view.pager.Top = at
		m.view.pager.SetCursor(at, m.viewHeight())
	}
}

// fileLine returns the line of the file the cursor is on, or the one
// before it that shows one, or 0.
func (m *Model) fileLine() int {
	for i := m.view.pager.Cursor; i >= 0; i-- {
		if i < len(m.view.spans) && m.view.spans[i].From > 0 {
			return m.view.spans[i].From
		}
		if r, ok := m.ref(i); ok && r.New > 0 {
			return r.New
		}
	}
	return 0
}

// stepView moves to the next or previous changed file.
func (m *Model) stepView(dir int) {
	if len(m.changes) == 0 {
		return
	}
	i := -1
	for j, c := range m.changes {
		if c.Path == m.view.path {
			i = j
		}
	}
	i += dir
	if i < 0 || i >= len(m.changes) {
		m.say("no more changed files")
		return
	}
	m.changeCursor = i
	if m.view.diff {
		m.openDiff(m.changes[i])
	} else {
		m.openFile(m.changes[i].Path)
	}
}

func (m *Model) viewKey(k tea.KeyPressMsg) tea.Cmd {
	p, h := &m.view.pager, m.viewHeight()

	if in := m.view.search; in != nil {
		switch k.String() {
		case "enter":
			p.Search(in.Value())
			m.view.search = nil
		case "esc":
			m.view.search = nil
		default:
			in.Key(k)
		}
		return nil
	}

	switch k.String() {
	case "esc":
		if p.Selecting() {
			p.Unselect()
			return nil
		}
		fallthrough
	case "q":
		m.mode = modeWorkspace
		m.seen()
	case "j", "down":
		p.Move(1, h)
	case "k", "up":
		p.Move(-1, h)
	case "v":
		p.Select()
	case "c":
		m.askNote()
	case "x":
		m.dropNote()
	case "t":
		return m.askSendNotes()
	case "space", " ", "f", "pgdown":
		p.Scroll(h-1, h)
	case "b", "pgup":
		p.Scroll(-(h - 1), h)
	case "g", "home":
		p.Move(-p.Len(), h)
	case "G", "end":
		p.Move(p.Len(), h)
	case "]", "[":
		dir := 1
		if k.String() == "[" {
			dir = -1
		}
		if !p.Mark(dir) {
			if m.view.rendered {
				m.say("no more headings")
			} else {
				m.say("no more hunks")
			}
		}
	case "/":
		m.view.search = &ui.Input{Prompt: "/"}
	case "n":
		p.NextMatch(1)
	case "N":
		p.NextMatch(-1)
	case "tab":
		m.stepView(1)
	case "shift+tab":
		m.stepView(-1)
	case "d":
		m.toggleView()
	case "m":
		m.toggleRendered()
	case "w":
		// The line the cursor is on stays on screen. A document goes back
		// to being wrapped the next time one is opened.
		p.Wrap = !p.Wrap
		if !m.view.rendered {
			m.view.wrap = p.Wrap
		}
		p.SetCursor(p.Cursor, h)
	case "e":
		return m.edit(m.view.path)
	case "o":
		return m.open(m.view.path)
	case "@":
		m.mode = modeWorkspace
		m.mention(m.view.path)
	}
	return nil
}
