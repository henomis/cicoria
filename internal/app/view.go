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
	"path/filepath"
	"strings"
	"time"

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
	// left and right say whether the panels on that side are shown; panels
	// whether any are.
	left, right, panels bool
	// body is everything above the status bar.
	body rect
	// sessions, files, changes and queue are the side panels, borders
	// included. queue is empty unless the session on screen has messages
	// waiting.
	sessions, files, changes, queue rect
	// agentBox is the agent pane with its border; agent is the terminal
	// inside it. Without side panels there is no border and they are equal,
	// unless the pane is split.
	agentBox, agent rect
	// otherBox and other are the same for the second session, when the agent
	// pane is split in two. They are empty when it is not.
	otherBox, other rect
}

// splitMin is the height below which the agent pane is not split.
const splitMin = 12

// inside returns the area within the border of a box.
func (r rect) inside() rect {
	return rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
}

// divide gives half of the agent pane to the second session, when there is
// one and the room for both.
func (m *Model) divide(l layout) layout {
	if m.splitWith() == nil || l.agentBox.h < splitMin {
		return l
	}
	box := l.agentBox
	upper := rect{box.x, box.y, box.w, (box.h + 1) / 2}
	lower := rect{box.x, box.y + upper.h, box.w, box.h - upper.h}
	l.agentBox, l.otherBox = upper, lower
	if m.below {
		l.agentBox, l.otherBox = lower, upper
	}
	l.agent, l.other = l.agentBox.inside(), l.otherBox.inside()
	return l
}

func (m *Model) layout() layout {
	l := layout{body: rect{0, 0, m.width, max(m.height-1, 1)}}
	l.agentBox, l.agent = l.body, l.body

	left, right := 28, 36
	if m.width < 120 {
		left, right = 24, 28
	}
	if m.hideLeft {
		left = 0
	}
	if m.hideRight {
		right = 0
	}
	if left+right == 0 || m.width < left+right+30 || l.body.h < 8 {
		return m.divide(l)
	}
	l.left, l.right, l.panels = left > 0, right > 0, true

	if l.left {
		sessionsH := max(min(len(m.sessions)+2, l.body.h/2), 3)
		l.sessions = rect{0, 0, left, sessionsH}
		l.files = rect{0, sessionsH, left, l.body.h - sessionsH}
	}
	if l.right {
		l.changes = rect{m.width - right, 0, right, l.body.h}
		if s := m.current(); s != nil && len(s.Queue) > 0 {
			queueH := max(min(len(s.Queue)+2, l.body.h/3), 3)
			l.changes.h -= queueH
			l.queue = rect{l.changes.x, l.changes.h, right, queueH}
		}
	}
	l.agentBox = rect{left, 0, m.width - left - right, l.body.h}
	l.agent = l.agentBox.inside()
	return m.divide(l)
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
		if m.mode == modePalette && m.paletteFrom == modeView {
			rows = m.renderView()
		} else {
			rows = m.renderWorkspace(l)
		}
		if m.mode == modePalette {
			w := min(m.width-4, 72)
			h := min(l.body.h-2, 18)
			box := m.palette.View("", w, h)
			rows = ui.Overlay(rows, box, (m.width-w)/2, max((l.body.h-h)/3, 0))
		} else if s := m.current(); s != nil && s.Term != nil && m.focus == focusAgent && m.typing() && m.anim == nil {
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
	return m.confirm == nil && m.prompt == nil && !m.prefix
}

func (m *Model) renderWorkspace(l layout) []string {
	agentRows := m.renderAgent(l)
	if l.other.h > 0 {
		otherRows := m.renderPane(m.splitWith(), l.otherBox, l.other, false)
		if m.below {
			agentRows = append(otherRows, agentRows...)
		} else {
			agentRows = append(agentRows, otherRows...)
		}
	}
	if !l.panels {
		return agentRows
	}
	rows := agentRows
	if l.left {
		left := append(m.renderSessions(l.sessions), m.renderFiles(l.files)...)
		rows = ui.JoinColumns(left, rows)
	}
	if l.right {
		right := m.renderChanges(l.changes)
		if l.queue.h > 0 {
			right = append(right, m.renderQueue(l.queue)...)
		}
		rows = ui.JoinColumns(rows, right)
	}
	return rows
}

func (m *Model) renderAgent(l layout) []string {
	s := m.current()

	var lines []string
	title := ""
	switch {
	case m.anim != nil:
		title = m.anim.title
		p := float64(time.Since(m.anim.start)) / float64(animFor)
		lines = m.anim.scene(min(p, 1), l.agent.w, l.agent.h)
	case s == nil:
		lines = []string{"", ui.Dim.Render(fmt.Sprintf("  no sessions — %s c starts one", m.cfg.Prefix))}
	default:
		return m.renderPane(s, l.agentBox, l.agent, m.focus == focusAgent)
	}

	if l.agent == l.agentBox {
		return ui.Block(lines, l.agent.w, l.agent.h)
	}
	return ui.Box(title, lines, l.agentBox.w, l.agentBox.h, m.focus == focusAgent)
}

// renderPane draws the screen of a session's agent in a pane, under a title
// that says what it is and how it is doing.
func (m *Model) renderPane(s *session.Session, box, inner rect, focused bool) []string {
	if s.Term == nil {
		lines := []string{""}
		if s.Err != nil {
			lines = append(lines, ui.Bad.Render("  could not start: "+s.Err.Error()))
		}
		if inner == box {
			return ui.Block(lines, inner.w, inner.h)
		}
		return ui.Box(s.Name, lines, box.w, box.h, focused)
	}
	lines := strings.Split(s.Term.Render(), "\n")
	if inner == box {
		return ui.Block(lines, inner.w, inner.h)
	}
	status := s.Status.String()
	if s.Stalled(time.Now()) {
		status += "?"
	}
	// Before the directory, which is what a narrow pane cuts off.
	if back := s.Term.Scrolled(); back > 0 {
		status = fmt.Sprintf("%s · %d lines back", status, back)
	}
	head := fmt.Sprintf("%s · %s · %s", s.Name, s.Adapter.Name(), status)
	// The branch that is known is that of the project in the side panels.
	branch := ""
	if s.Dir == m.shown {
		branch = m.branch
	}
	return ui.Box(paneTitle(head, tilde(s.Dir), branch, box.w-6), lines, box.w, box.h, focused)
}

// paneTitle heads the agent pane with the session, its directory and the
// branch that directory is on, in the room there is. The directory gives way
// first, keeping its end, then the branch, and what is left of the directory
// last.
func paneTitle(head, dir, branch string, room int) string {
	if branch == "" {
		return head + " · " + dir
	}
	free := room - ansi.StringWidth(head+" ·  · ")
	if w := ansi.StringWidth(dir); w+ansi.StringWidth(branch) > free {
		// The end of a path says more than its start.
		if keep := max(free-ansi.StringWidth(branch), min(w, minPart)); keep < w {
			dir = "…" + ansi.TruncateLeft(dir, w-keep+1, "")
		}
		branch = middle(branch, max(free-ansi.StringWidth(dir), minPart))
	}
	if ansi.StringWidth(dir)+ansi.StringWidth(branch) > free {
		return head + " · " + branch
	}
	return head + " · " + dir + " · " + branch
}

// minPart is how much of a directory and of a branch's name is shown before
// the other gives way.
const minPart = 12

// middle cuts a text that is wider than width in the middle, so that both
// its start and its end can be read.
func middle(s string, width int) string {
	w := ansi.StringWidth(s)
	if w <= width {
		return s
	}
	tail := (width - 1) / 2
	return ansi.Truncate(s, width-tail, "…") + ansi.TruncateLeft(s, w-tail, "")
}

// badge is the status mark shown next to a session.
func badge(s *session.Session) string {
	switch s.Status {
	case agent.Working:
		if s.Stalled(time.Now()) {
			return ui.Dim.Render("●")
		}
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

// lasting tells for how long a session has been in its status, once that is
// a minute or more.
func lasting(s *session.Session) string {
	d := time.Since(s.Since)
	switch {
	case s.Since.IsZero() || d < time.Minute:
		return ""
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 100*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

func (m *Model) renderSessions(r rect) []string {
	focused := m.focus == focusSessions
	// The list scrolls to keep the cursor in sight, or the session on screen
	// while the panel does not have the keys.
	cursor := m.active
	if focused {
		cursor = m.sessionCursor
	}
	m.sessionTop = ui.Window(cursor, m.sessionTop, r.h-2, len(m.sessions))
	var lines []string
	for i := m.sessionTop; i < len(m.sessions); i++ {
		s := m.sessions[i]
		num := " "
		if i < 9 {
			num = fmt.Sprint(i + 1)
		}
		name := s.Name
		if i == m.active || s == m.other() {
			name = ui.Bold.Render(name)
		}
		row := fmt.Sprintf("%s %s %s", ui.Dim.Render(num), badge(s), name)
		if n := len(s.Queue); n > 0 {
			row += ui.Dim.Render(fmt.Sprintf(" ⋯%d", n))
		}
		// How long it has been so and its project go at the right edge, as
		// far as there is room: the time first.
		for _, tail := range []string{strings.TrimSpace(lasting(s) + " " + filepath.Base(s.Dir)), lasting(s)} {
			if tail != "" && ansi.StringWidth(row)+ansi.StringWidth(tail)+1 < r.w-2 {
				row = ui.Fit(row, r.w-2-ansi.StringWidth(tail)) + ui.Dim.Render(tail)
				break
			}
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
	tree := m.tree()
	rows := tree.Rows()
	tree.Top = ui.Window(tree.Cursor, tree.Top, height, len(rows))

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
	for i := tree.Top; i < min(tree.Top+height, len(rows)); i++ {
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
		switch {
		case row.Ignored:
			name = ui.Dim.Render(name)
		case row.Dir:
			name = ui.Info.Render(name)
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
		lines = append(lines, ui.Selected(text, inner, i == tree.Cursor, focused))
	}
	if len(rows) == 0 && !m.loading {
		lines = []string{ui.Dim.Render(" no files")}
	}
	return ui.Box("Files · "+filepath.Base(m.project()), lines, r.w, r.h, focused)
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
	case m.loading:
		lines = nil
	case !m.repo:
		lines = []string{ui.Dim.Render(" not a git repository")}
	case len(m.changes) == 0:
		lines = []string{ui.Dim.Render(" working tree clean")}
	default:
		title = fmt.Sprintf("Changes (%d)", len(m.changes))
	}
	// How many commits the changes span, when they are counted from where
	// the session started.
	if s := m.current(); !m.loading && m.repo && s != nil && s.Base != "" && !s.Uncommitted {
		switch {
		case m.commits == 1:
			title += " · 1 commit"
		case m.commits > 1:
			title += fmt.Sprintf(" · %d commits", m.commits)
		}
	}
	return ui.Box(title, lines, r.w, r.h, focused)
}

func (m *Model) renderView() []string {
	kind := "file"
	switch {
	case m.view.diff:
		kind = "diff"
	case m.view.rendered:
		kind = "rendered"
	}
	header := fmt.Sprintf(" %s  %s", kind, m.view.path)
	if where := ui.Dim.Render(tilde(m.view.dir)) + " "; ansi.StringWidth(header)+ansi.StringWidth(where) < m.width {
		header = ui.Fit(header, m.width-ansi.StringWidth(where)) + where
	}
	header = ui.Reverse.Render(ansi.Strip(ui.Fit(header, m.width)))
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
		lines = append(lines, fmt.Sprintf("    %s %s %s", ui.Accent.Render(p), ui.Accent.Render(fmt.Sprintf("%-6s", c.key)), c.name))
	}
	lines = append(lines,
		fmt.Sprintf("    %s %s %s", ui.Accent.Render(p), ui.Accent.Render(fmt.Sprintf("%-6s", "1-9")), "Go to session by number"),
		fmt.Sprintf("    %s %s %s", ui.Accent.Render(p), ui.Accent.Render(fmt.Sprintf("%-6s", p)), "Send "+p+" to the agent"),
		"    These work in a full-screen file or diff too.",
		"",
		ui.Bold.Render("  New session"),
		"    pick the agent, the directory it works in and a name: tab completes the",
		"    directory, ctrl+w deletes a path component, ctrl+u clears. The side panels",
		"    follow the session on screen.",
		"    Where another session already works, the new one is offered a git worktree",
		"    of its own, <repository>.worktrees/<name> on the branch cicoria/<name>:",
		"    y takes it, n or enter does not. A shell does not count: it is not offered",
		"    one, and is no reason to offer one.",
		"    Closing the session offers to remove it; the branch is kept.",
		"",
		ui.Bold.Render("  Workspaces"),
		"    sessions are saved for the directory cicoria was opened on, wherever they",
		"    work, and only come back when it is opened on that directory again.",
		"    "+p+" w opens another saved workspace in place of this one.",
		"",
		ui.Bold.Render("  Scrolling back"),
		"    the mouse wheel and pgup/pgdn scroll through what has left the agent's",
		"    screen; typing goes back to the end. An agent that reads the mouse or",
		"    takes the whole screen, like an editor, scrolls by itself.",
		"",
		ui.Bold.Render("  Two sessions on screen"),
		"    "+p+" - shows another session below the one on screen; again, and",
		"    there is one pane. "+p+" tab or a click moves the keys between the",
		"    panes. Going to a session puts it in the pane that has the keys.",
		"",
		ui.Bold.Render("  Queue"),
		"    messages wait in the queue and go to the agent one by one, each when it",
		"    ends its turn; to an idle agent the first goes at once. Agents that do not",
		"    report their status only get a message when you press enter in the queue.",
		"    queue panel: enter send the next · p pause · e edit · x delete · m add",
		"                 J/K move a message down/up",
		"    "+p+" k queues a message for another session, or asks it: what its",
		"    agent says at the end of that turn comes back to this session with your",
		"    question, and with what you want done with the answer, if you say so.",
		"    Claude Code and Codex CLI can be asked.",
		"",
		ui.Bold.Render("  In a side panel"),
		"    j/k move · enter open · tab next panel · esc back to the agent",
		"    sessions: enter go to it · , rename · r restart · x close · c new",
		"    files: h/l close/open folder · d diff · e editor · @ mention to the agent",
		"           o open with the system, as an image or a web page wants",
		"           i show ⇄ hide what git ignores, which is grey",
		"    changes: enter/d diff · v file · e editor · o open · @ mention",
		"             s what is not committed ⇄ everything since the session started",
		"",
		ui.Bold.Render("  In a full-screen file or diff"),
		"    j/k space b g G scroll · / search · n N matches · [ ] hunks",
		"    tab/shift+tab next/previous changed file · d file ⇄ diff · q back",
		"    e editor · o open with the system · @ mention to the agent",
		"    w wrap long lines ⇄ cut them",
		"    m a Markdown file as a document ⇄ its text · [ ] headings of a document",
		"    v select lines · c write a note on them · x delete it · t send the notes",
		"",
		ui.Bold.Render("  Notes"),
		"    a note is a remark on one or more lines of a file or diff. They wait,",
		"    marked with * in the margin, until you send them: together, as one",
		"    message with the file, the lines and their text, at once or through the",
		"    queue.",
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
	case m.prompt != nil:
		// When the text outgrows the line, keep its end, where the typing is.
		text := " " + m.prompt.input.View()
		if over := ansi.StringWidth(text) - m.width; over > 0 {
			text = ansi.TruncateLeft(text, over+1, "…")
		}
		return ui.Fit(text, m.width)
	case m.mode == modeView && m.view.search != nil:
		return ui.Fit(" "+m.view.search.View(), m.width)
	}

	// A palette over the full-screen view leaves its status on screen.
	under := m.mode
	if under == modePalette {
		under = m.paletteFrom
	}

	var left []string
	if m.prefix {
		left = append(left, ui.Reverse.Render(" "+m.cfg.Prefix+" "))
	}
	if m.message != "" {
		left = append(left, m.message)
	}
	for i, s := range m.sessions {
		if s.Status == agent.NeedsInput && (i != m.active || under != modeWorkspace) {
			left = append(left, ui.Warn.Render("▲ "+s.Name+" needs input"))
		}
	}

	var right string
	switch under {
	case modeView:
		left = append(left, m.view.pager.Status(m.viewHeight()))
		if s := m.current(); s != nil && len(s.Notes) > 0 {
			left = append(left, ui.Warn.Render(fmt.Sprintf("%d note(s)", len(s.Notes))))
		}
		right = "q back · / search · [ ] hunks · tab file · d diff · v select · c note · t send"
		if m.mode == modePalette {
			right = "esc back"
		}
	default:
		if s := m.current(); s != nil {
			if s.Status == agent.Exited {
				left = append(left, fmt.Sprintf("%s exited — %s r restarts it, %s x closes it", s.Name, m.cfg.Prefix, m.cfg.Prefix))
			}
			if n := len(s.Queue); n > 0 {
				right = fmt.Sprintf("queue %d%s · ", n, map[bool]string{true: " paused"}[s.Paused])
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
