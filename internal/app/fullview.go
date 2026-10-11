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
	m.view.pager.Title = c.Path
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

// openFile shows a file on the whole screen.
func (m *Model) openFile(path string) {
	m.view.dir, m.view.path, m.view.diff = m.project(), path, false
	m.view.pager.Title = path
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
	full := filepath.Join(m.view.dir, path)
	st, err := os.Stat(full)
	if err != nil {
		return []string{ui.Bad.Render(err.Error())}, nil
	}
	if st.IsDir() {
		return []string{ui.Dim.Render("a directory")}, nil
	}

	f, err := os.Open(full)
	if err != nil {
		return []string{ui.Bad.Render(err.Error())}, nil
	}
	defer func() { _ = f.Close() }()
	data := make([]byte, min(st.Size(), maxFile))
	n, _ := f.Read(data)
	data = data[:n]

	if ui.IsBinary(data) {
		return []string{ui.Dim.Render(fmt.Sprintf("binary file, %d bytes", st.Size()))}, nil
	}
	lines = ui.FileLines(path, string(data), m.dark)
	source = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(source) != len(lines) {
		source = nil
	}
	if st.Size() > maxFile {
		lines = append(lines, ui.Warn.Render(fmt.Sprintf("… only the first %d MB are shown", maxFile>>20)))
	}
	return lines, source
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

// askNote asks for a note on the selected lines, or for a new text for the
// note the cursor is on.
func (m *Model) askNote() {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
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
		return m.enqueue(s, text)
	}
	m.save()
	return m.deliver(s, text)
}

// reloadView reads again what the full-screen view shows, keeping its place.
func (m *Model) reloadView() {
	top, cursor := m.view.pager.Top, m.view.pager.Cursor
	if c, ok := m.change(m.view.path); m.view.diff && ok {
		m.openDiff(c)
	} else {
		m.openFile(m.view.path)
	}
	m.view.pager.Top = top
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
	case "s":
		return m.askSendNotes()
	case "space", " ", "f", "pgdown":
		p.Scroll(h-1, h)
	case "b", "pgup":
		p.Scroll(-(h - 1), h)
	case "g", "home":
		p.Move(-p.Len(), h)
	case "G", "end":
		p.Move(p.Len(), h)
	case "]":
		if !p.Mark(1) {
			m.say("no more hunks")
		}
	case "[":
		if !p.Mark(-1) {
			m.say("no more hunks")
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
	case "w":
		// The line the cursor is on stays on screen.
		p.Wrap = !p.Wrap
		p.SetCursor(p.Cursor, h)
	case "e":
		return m.edit(m.view.path)
	case "@":
		m.mode = modeWorkspace
		m.mention(m.view.path)
	}
	return nil
}
