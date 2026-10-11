package app

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/git"
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
	// search is set while the user types a search.
	search *ui.Input
}

// viewHeight is the number of text rows of the full-screen view.
func (m *Model) viewHeight() int { return max(m.height-2, 1) }

// openDiff shows the diff of a changed file on the whole screen.
func (m *Model) openDiff(c git.Change) {
	m.view.dir, m.view.path, m.view.diff = m.project(), c.Path, true
	m.view.pager.Title = c.Path
	m.mode = modeView

	diff, err := git.Diff(m.view.dir, c)
	switch {
	case err != nil:
		m.view.pager.SetContent([]string{ui.Bad.Render(err.Error())}, nil)
	case diff == "":
		m.view.pager.SetContent([]string{ui.Dim.Render("no textual differences")}, nil)
	default:
		m.view.pager.SetContent(ui.DiffLines(diff))
	}
}

// openFile shows a file on the whole screen.
func (m *Model) openFile(path string) {
	m.view.dir, m.view.path, m.view.diff = m.project(), path, false
	m.view.pager.Title = path
	m.mode = modeView
	m.view.pager.SetContent(m.fileLines(path), nil)
}

func (m *Model) fileLines(path string) []string {
	full := filepath.Join(m.view.dir, path)
	st, err := os.Stat(full)
	if err != nil {
		return []string{ui.Bad.Render(err.Error())}
	}
	if st.IsDir() {
		return []string{ui.Dim.Render("a directory")}
	}

	f, err := os.Open(full)
	if err != nil {
		return []string{ui.Bad.Render(err.Error())}
	}
	defer func() { _ = f.Close() }()
	data := make([]byte, min(st.Size(), maxFile))
	n, _ := f.Read(data)
	data = data[:n]

	if ui.IsBinary(data) {
		return []string{ui.Dim.Render(fmt.Sprintf("binary file, %d bytes", st.Size()))}
	}
	lines := ui.FileLines(path, string(data), m.dark)
	if st.Size() > maxFile {
		lines = append(lines, ui.Warn.Render(fmt.Sprintf("… only the first %d MB are shown", maxFile>>20)))
	}
	return lines
}

// reloadView reads again what the full-screen view shows, keeping its place.
func (m *Model) reloadView() {
	top := m.view.pager.Top
	if c, ok := m.change(m.view.path); m.view.diff && ok {
		m.openDiff(c)
	} else {
		m.openFile(m.view.path)
	}
	m.view.pager.Scroll(top, m.viewHeight())
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
	case "q", "esc":
		m.mode = modeWorkspace
		m.seen()
	case "j", "down":
		p.Scroll(1, h)
	case "k", "up":
		p.Scroll(-1, h)
	case "space", " ", "f", "pgdown":
		p.Scroll(h-1, h)
	case "b", "pgup":
		p.Scroll(-(h - 1), h)
	case "g", "home":
		p.Scroll(-p.Len(), h)
	case "G", "end":
		p.Scroll(p.Len(), h)
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
	case "e":
		return m.edit(m.view.path)
	case "@":
		m.mode = modeWorkspace
		m.mention(m.view.path)
	}
	return nil
}
