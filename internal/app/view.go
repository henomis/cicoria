package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// rect is an area of the screen.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// layout is where everything goes for the current window size.
type layout struct {
	// panels says whether the side panels are shown.
	panels bool
	// body is everything above the status bar.
	body rect
	// sessions, files and changes are the side panels, borders included.
	sessions, files, changes rect
	// agentBox is the agent pane with its border; agent is the terminal
	// inside it. Without side panels there is no border and they are equal.
	agentBox, agent rect
}

func (m *Model) layout() layout {
	l := layout{body: rect{0, 0, m.width, max(m.height-1, 1)}}
	l.agentBox, l.agent = l.body, l.body

	left, right := 28, 36
	if m.width < 120 {
		left, right = 24, 28
	}
	if m.hidePanels || m.width < left+right+30 || l.body.h < 8 {
		return l
	}
	l.panels = true

	sessionsH := max(min(len(m.sessions)+2, l.body.h/2), 3)
	l.sessions = rect{0, 0, left, sessionsH}
	l.files = rect{0, sessionsH, left, l.body.h - sessionsH}
	l.changes = rect{m.width - right, 0, right, l.body.h}
	l.agentBox = rect{left, 0, m.width - left - right, l.body.h}
	l.agent = rect{left + 1, 1, l.agentBox.w - 2, l.agentBox.h - 2}
	return l
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.WindowTitle = "cicoria"
	if m.width == 0 || m.height == 0 {
		return v
	}

	var rows []string
	switch m.mode {
	case modeView:
		rows = m.renderView()
	case modeHelp:
		rows = m.renderHelp()
	default:
		l := m.layout()
		rows = m.renderWorkspace(l)
		if m.mode == modePalette {
			w := min(m.width-4, 72)
			h := min(l.body.h-2, 18)
			box := m.palette.View("", w, h)
			rows = ui.Overlay(rows, box, (m.width-w)/2, max((l.body.h-h)/3, 0))
		} else if s := m.current(); s != nil && s.Term != nil && m.focus == focusAgent && m.typing() {
			if x, y, visible := s.Term.Cursor(); visible && s.Status != agent.Exited {
				v.Cursor = tea.NewCursor(l.agent.x+x, l.agent.y+y)
			}
		}
	}

	rows = append(rows[:min(len(rows), m.height-1)], m.renderStatus())
	v.SetContent(strings.Join(rows, "\n"))
	return v
}

// typing reports whether keys go straight to the agent.
func (m *Model) typing() bool {
	return m.confirm == nil && m.rename == nil && !m.prefix
}

func (m *Model) renderWorkspace(l layout) []string {
	agentRows := m.renderAgent(l)
	if !l.panels {
		return agentRows
	}
	left := append(m.renderSessions(l.sessions), m.renderFiles(l.files)...)
	return ui.JoinColumns(left, agentRows, m.renderChanges(l.changes))
}

func (m *Model) renderAgent(l layout) []string {
	s := m.current()

	var lines []string
	title := ""
	switch {
	case s == nil:
		lines = []string{"", ui.Dim.Render(fmt.Sprintf("  no sessions — %s c starts one", m.cfg.Prefix))}
	case s.Term == nil:
		title = s.Name
		lines = []string{""}
		if s.Err != nil {
			lines = append(lines, ui.Bad.Render("  could not start: "+s.Err.Error()))
		}
	default:
		title = fmt.Sprintf("%s · %s · %s", s.Name, s.Adapter.Name(), s.Status)
		lines = strings.Split(s.Term.Render(), "\n")
	}

	if !l.panels {
		return ui.Block(lines, l.agent.w, l.agent.h)
	}
	return ui.Box(title, lines, l.agentBox.w, l.agentBox.h, m.focus == focusAgent)
}

// badge is the status mark shown next to a session.
func badge(s *session.Session) string {
	switch s.Status {
	case agent.Working:
		return ui.Good.Render("●")
	case agent.NeedsInput:
		return ui.Warn.Render("▲")
	case agent.Exited:
		return ui.Bad.Render("✕")
	case agent.Idle:
		if s.Unseen {
			return ui.Good.Render("✓")
		}
		return ui.Dim.Render("○")
	}
	return ui.Dim.Render("◌")
}

func (m *Model) renderSessions(r rect) []string {
	focused := m.focus == focusSessions
	var lines []string
	for i, s := range m.sessions {
		num := " "
		if i < 9 {
			num = fmt.Sprint(i + 1)
		}
		row := fmt.Sprintf("%s %s %s", ui.Dim.Render(num), badge(s), s.Name)
		if i == m.active {
			row = fmt.Sprintf("%s %s %s", ui.Dim.Render(num), badge(s), ui.Bold.Render(s.Name))
		}
		lines = append(lines, ui.Selected(row, r.w-2, focused && i == m.sessionCursor, true))
	}
	return ui.Box("Sessions", lines, r.w, r.h, focused)
}

// mark renders the letter of a git status in its colour.
func mark(letter string) string {
	switch letter {
	case "M", "R":
		return ui.Warn.Render(letter)
	case "A", "?":
		return ui.Good.Render(letter)
	case "D", "U":
		return ui.Bad.Render(letter)
	}
	return letter
}

func (m *Model) renderFiles(r rect) []string {
	focused := m.focus == focusFiles
	inner, height := r.w-2, r.h-2
	rows := m.tree.Rows()
	m.tree.Top = ui.Window(m.tree.Cursor, m.tree.Top, height, len(rows))

	marks := map[string]string{}
	dirty := map[string]bool{}
	for _, c := range m.changes {
		marks[c.Path] = c.Mark()
		for dir := c.Path; strings.Contains(dir, "/"); {
			dir = dir[:strings.LastIndex(dir, "/")]
			dirty[dir] = true
		}
	}
	var touched map[string]bool
	if s := m.current(); s != nil {
		touched = map[string]bool{}
		for p := range s.Touched {
			touched[p] = true
		}
	}

	var lines []string
	for i := m.tree.Top; i < min(m.tree.Top+height, len(rows)); i++ {
		row := rows[i]
		name := row.Name
		tail := ""
		switch {
		case row.Dir && row.Open:
			name = "▾ " + name + "/"
		case row.Dir:
			name = "▸ " + name + "/"
		default:
			name = "  " + name
		}
		if row.Dir && dirty[row.Path] {
			tail = ui.Dim.Render("•")
		} else if mk, ok := marks[row.Path]; ok {
			tail = mark(mk)
		}
		if touched[row.Path] {
			name = ui.Accent.Render(name)
		}

		text := strings.Repeat(" ", row.Depth) + name
		if tail != "" {
			text = ui.Fit(text, inner-2) + " " + tail
		}
		lines = append(lines, ui.Selected(text, inner, i == m.tree.Cursor, focused))
	}
	if len(rows) == 0 {
		lines = []string{ui.Dim.Render(" no files")}
	}
	return ui.Box("Files", lines, r.w, r.h, focused)
}

func (m *Model) renderChanges(r rect) []string {
	focused := m.focus == focusChanges
	inner, height := r.w-2, r.h-2
	m.changeTop = ui.Window(m.changeCursor, m.changeTop, height, len(m.changes))

	var lines []string
	for i := m.changeTop; i < min(m.changeTop+height, len(m.changes)); i++ {
		c := m.changes[i]
		stat := ""
		if c.Added > 0 {
			stat += ui.Good.Render(fmt.Sprintf(" +%d", c.Added))
		}
		if c.Deleted > 0 {
			stat += ui.Bad.Render(fmt.Sprintf(" -%d", c.Deleted))
		}
		// Long paths lose their start, not the file name.
		path := c.Path
		if room := inner - 2 - ansi.StringWidth(stat); ansi.StringWidth(path) > room && room > 1 {
			path = ansi.TruncateLeft(path, ansi.StringWidth(path)-room+1, "…")
		}
		text := mark(c.Mark()) + " " + ui.Fit(path, max(inner-2-ansi.StringWidth(stat), 1)) + stat
		lines = append(lines, ui.Selected(text, inner, i == m.changeCursor, focused))
	}

	title := "Changes"
	switch {
	case !m.repo:
		lines = []string{ui.Dim.Render(" not a git repository")}
	case len(m.changes) == 0:
		lines = []string{ui.Dim.Render(" working tree clean")}
	default:
		title = fmt.Sprintf("Changes (%d)", len(m.changes))
	}
	return ui.Box(title, lines, r.w, r.h, focused)
}

func (m *Model) renderView() []string {
	kind := "file"
	if m.view.diff {
		kind = "diff"
	}
	header := ui.Reverse.Render(ui.Fit(fmt.Sprintf(" %s  %s", kind, m.view.path), m.width))
	return append([]string{header}, m.view.pager.View(m.width, m.viewHeight())...)
}

func (m *Model) renderHelp() []string {
	p := m.cfg.Prefix
	lines := []string{
		"",
		ui.Bold.Render("  Everything you type goes to the agent. " + p + " starts a command:"),
		"",
	}
	for _, c := range commands {
		lines = append(lines, fmt.Sprintf("    %s %-6s %s", ui.Accent.Render(p), ui.Accent.Render(c.key), c.name))
	}
	lines = append(lines,
		fmt.Sprintf("    %s %-6s %s", ui.Accent.Render(p), ui.Accent.Render("1-9"), "Go to session by number"),
		fmt.Sprintf("    %s %-6s %s", ui.Accent.Render(p), ui.Accent.Render(p), "Send "+p+" to the agent"),
		"",
		ui.Bold.Render("  In a side panel"),
		"    j/k move · enter open · tab next panel · esc back to the agent",
		"    files: h/l close/open folder · d diff · e editor · @ mention to the agent",
		"    changes: enter diff · v file · e editor · @ mention to the agent",
		"",
		ui.Bold.Render("  In a full-screen file or diff"),
		"    j/k space b g G scroll · / search · n N matches · [ ] hunks",
		"    tab/shift+tab next/previous changed file · d file ⇄ diff · e editor · q back",
		"",
		ui.Dim.Render("  press any key to close"),
	)
	return ui.Box("Help", lines, m.width, max(m.height-1, 2), true)
}

// renderStatus draws the bottom line.
func (m *Model) renderStatus() string {
	switch {
	case m.confirm != nil:
		return ui.Fit(ui.Warn.Render(" "+m.confirm.question)+" [y/N]", m.width)
	case m.rename != nil:
		return ui.Fit(" "+m.rename.View(), m.width)
	case m.mode == modeView && m.view.search != nil:
		return ui.Fit(" "+m.view.search.View(), m.width)
	}

	var left []string
	if m.prefix {
		left = append(left, ui.Reverse.Render(" "+m.cfg.Prefix+" "))
	}
	if m.message != "" {
		left = append(left, m.message)
	}
	for i, s := range m.sessions {
		if s.Status == agent.NeedsInput && (i != m.active || m.mode != modeWorkspace) {
			left = append(left, ui.Warn.Render("▲ "+s.Name+" needs input"))
		}
	}

	var right string
	switch m.mode {
	case modeView:
		left = append(left, m.view.pager.Status(m.viewHeight()))
		right = "q back · / search · [ ] hunks · tab next file · d file⇄diff · e edit"
	default:
		if s := m.current(); s != nil {
			if s.Status == agent.Exited {
				left = append(left, fmt.Sprintf("%s exited — %s r restarts it, %s x closes it", s.Name, m.cfg.Prefix, m.cfg.Prefix))
			}
			if s.ContextTokens > 0 {
				right = fmt.Sprintf("ctx %dk · ", (s.ContextTokens+500)/1000)
			}
		}
		right += m.cfg.Prefix + " ? help"
	}

	text := " " + strings.Join(left, ui.Dim.Render(" · "))
	right = ui.Dim.Render(right) + " "
	gap := m.width - ansi.StringWidth(text) - ansi.StringWidth(right)
	if gap < 1 {
		return ui.Fit(text, m.width)
	}
	return text + strings.Repeat(" ", gap) + right
}
