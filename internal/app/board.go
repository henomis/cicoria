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
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// The board shows the backlog next to every session, in a column for what
// each asks of the user. Only the backlog is the user's to arrange: where a
// session is follows from its state, so it moves when that does, and never
// because it was moved. Nothing on the board starts by itself.

// columns is how many columns the board has: the backlog, then the lanes.
const columns = 1 + session.Lanes

// columnTitles names the columns of the board.
var columnTitles = [columns]string{"Backlog", "▲ Needs you", "● Working", "✓ To review", "○ Settled"}

// cardRows is how many rows of text a card has on a wide board, where each
// card is in a frame of its own, and boardWide the width from which the
// columns are side by side.
const (
	cardRows  = 4
	cardFrame = cardRows + 2
	boardWide = 100
)

// board is what the board shows and where its cursor is.
type board struct {
	col, row int
	tops     [columns]int
	// on is the session under the cursor: when its card goes to another
	// column, the cursor goes with it.
	on *session.Session
	// peek says the screen of the session under the cursor is shown over
	// the board.
	peek bool
	// info is what was last read of the work of each session, by ID, and
	// polling says a look is under way.
	info    map[string]workInfo
	polling bool
}

// workInfo is what the board says of the work of a session.
type workInfo struct {
	branch         string
	files, commits int
}

// boardMsg carries a fresh look at the work of every session.
type boardMsg struct{ info map[string]workInfo }

// boardEntry is one card of the board: a card of the backlog or a session.
type boardEntry struct {
	card *session.Card
	s    *session.Session
	// i is its position in the backlog or among the sessions.
	i int
}

// cardAgent is an agent picked to start a card of the backlog with.
type cardAgent struct {
	a    agent.Adapter
	card session.Card
}

// openBoard shows the board, or goes back from it.
func (m *Model) openBoard() tea.Cmd {
	if m.mode == modeBoard {
		m.closeBoard()
		return nil
	}
	m.mode, m.board.peek = modeBoard, false
	// The cursor starts on what needs the user, if anything does.
	if len(m.boardColumns()[1]) > 0 {
		m.board.col, m.board.row = 1, 0
	}
	m.noteCursor()
	return m.pollBoard()
}

// closeBoard goes back from the board to the workspace.
func (m *Model) closeBoard() {
	m.mode, m.board.peek = modeWorkspace, false
	m.seen()
}

// changed says whether a session has work to look at, as last read.
func (m *Model) changed(s *session.Session) bool {
	w := m.board.info[s.ID]
	return !isShell(s.Adapter) && (w.files > 0 || w.commits > 0)
}

// boardColumns returns the cards of each column of the board.
func (m *Model) boardColumns() [columns][]boardEntry {
	var cols [columns][]boardEntry
	for i := range m.cards {
		cols[0] = append(cols[0], boardEntry{card: &m.cards[i], i: i})
	}
	for i, s := range m.sessions {
		lane := 1 + int(s.Lane(m.changed(s)))
		cols[lane] = append(cols[lane], boardEntry{s: s, i: i})
	}
	return cols
}

// boardItem returns the card under the cursor, which is empty when its
// column is.
func (m *Model) boardItem() boardEntry {
	cols := m.boardColumns()
	m.board.col = max(min(m.board.col, columns-1), 0)
	col := cols[m.board.col]
	m.board.row = max(min(m.board.row, len(col)-1), 0)
	if len(col) == 0 {
		return boardEntry{i: -1}
	}
	return col[m.board.row]
}

// boardCursorOn puts the cursor of the board on the card of a session.
func (m *Model) boardCursorOn(s *session.Session) {
	for c, col := range m.boardColumns() {
		for r, e := range col {
			if e.s == s {
				m.board.col, m.board.row, m.board.on = c, r, s
				return
			}
		}
	}
}

// followCursor puts the cursor back on its session, wherever its card is
// now.
func (m *Model) followCursor() {
	if m.board.on != nil {
		m.boardCursorOn(m.board.on)
	}
}

// noteCursor remembers the session under the cursor, if a session is.
func (m *Model) noteCursor() {
	m.board.on = m.boardItem().s
}

// stepColumn moves the cursor to the column before or after. On a narrow
// board, which lists only the lanes that have cards, it skips the others.
func (m *Model) stepColumn(dir int) {
	cols := m.boardColumns()
	for c := m.board.col + dir; c >= 0 && c < columns; c += dir {
		if m.width >= boardWide || c == 0 || len(cols[c]) > 0 {
			m.board.col, m.board.row = c, 0
			return
		}
	}
}

// moveBoard moves the cursor by a number of cards. On a narrow board, where
// the columns are one list, it goes on into the next column that has cards.
func (m *Model) moveBoard(delta int) {
	cols := m.boardColumns()
	m.board.row += delta
	if m.width >= boardWide {
		return
	}
	for m.board.row >= len(cols[m.board.col]) && m.board.col < columns-1 {
		m.board.row -= max(len(cols[m.board.col]), 0)
		m.board.col++
	}
	for m.board.row < 0 && m.board.col > 0 {
		m.board.col--
		m.board.row += len(cols[m.board.col])
	}
}

// pollBoard reads, in the background, the work of every session: the
// branch it is on, and what changed and how many commits were made since it
// started.
func (m *Model) pollBoard() tea.Cmd {
	if m.board.polling {
		return nil
	}
	m.board.polling = true
	type look struct{ id, dir, base string }
	var looks []look
	for _, s := range m.sessions {
		looks = append(looks, look{s.ID, s.Dir, s.Base})
	}
	return func() tea.Msg {
		info := map[string]workInfo{}
		for _, l := range looks {
			base := l.base
			if base != "" && !git.Exists(l.dir, base) {
				base = ""
			}
			changes, err := git.Status(l.dir, base)
			if err != nil {
				continue
			}
			w := workInfo{branch: git.Branch(l.dir), files: len(changes)}
			if base != "" {
				w.commits = git.Commits(l.dir, base)
			}
			info[l.id] = w
		}
		return boardMsg{info}
	}
}

// boardKey handles the keys of the board.
func (m *Model) boardKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	m.followCursor()
	defer m.noteCursor()
	if m.board.peek {
		m.board.peek = false
		return nil
	}
	if m.prefix {
		m.prefix = false
		// The key that opened the board closes it.
		if key == "0" {
			m.closeBoard()
			return nil
		}
		return m.runPrefix(k)
	}
	if key == m.cfg.Prefix {
		m.prefix = true
		return nil
	}

	switch key {
	case "esc", "q":
		m.closeBoard()
	case "h", "left":
		m.stepColumn(-1)
	case "l", "right":
		m.stepColumn(1)
	case "j", "down":
		m.moveBoard(1)
	case "k", "up":
		m.moveBoard(-1)
	case "g", "home":
		m.board.row = 0
	case "G", "end":
		m.board.row = len(m.boardColumns()[m.board.col]) - 1
	case "a":
		if len(m.boardColumns()[1]) == 0 {
			m.say("no session needs you")
			return nil
		}
		// The next one, round the column.
		if m.board.col == 1 {
			m.board.row = (m.board.row + 1) % len(m.boardColumns()[1])
		} else {
			m.board.col, m.board.row = 1, 0
		}
	case "c":
		return m.editCard(nil)
	default:
		return m.boardItemKey(key)
	}
	return nil
}

// boardItemKey handles the keys for the card under the cursor.
func (m *Model) boardItemKey(key string) tea.Cmd {
	it := m.boardItem()
	switch {
	case it.card != nil:
		return m.cardKey(key, it)
	case it.s != nil:
		return m.boardSessionKey(key, it)
	}
	return nil
}

// cardKey handles the keys for a card of the backlog.
func (m *Model) cardKey(key string, it boardEntry) tea.Cmd {
	switch key {
	case "enter":
		return m.startCard(*it.card)
	case "e":
		return m.editCard(it.card)
	case "x":
		id := it.card.ID
		m.confirm = &confirm{
			question: "delete the card " + it.card.Title + "?",
			yes: func() tea.Cmd {
				m.dropCard(id)
				m.save()
				return nil
			},
		}
	case "J", "K":
		delta := 1
		if key == "K" {
			delta = -1
		}
		if j := it.i + delta; j >= 0 && j < len(m.cards) {
			m.cards[it.i], m.cards[j] = m.cards[j], m.cards[it.i]
			m.board.row = j
			m.save()
		}
	}
	return nil
}

// boardSessionKey handles the keys for a session on the board. The
// commands are those of the workspace, for the session under the cursor.
func (m *Model) boardSessionKey(key string, it boardEntry) tea.Cmd {
	switch key {
	case "enter":
		m.closeBoard()
		return m.switchTo(it.i)
	case "space", " ":
		m.board.peek = true
	case "m":
		return m.askQueueFor(it.s)
	case "d":
		// Its changes are listed in the workspace, from where they open.
		m.closeBoard()
		return tea.Batch(m.switchTo(it.i), m.setFocus(focusChanges))
	case "r":
		return m.restart()
	case "x":
		return m.askClose()
	case ",":
		return m.askRename()
	}
	return nil
}

// boardMouse handles the mouse on the board: a click puts the cursor on a
// card, and a click on the card under it acts as enter; the wheel moves.
func (m *Model) boardMouse(msg tea.MouseMsg) tea.Cmd {
	// A question or a prompt is about the card it was asked for.
	if m.board.peek || m.confirm != nil || m.prompt != nil {
		return nil
	}
	m.followCursor()
	defer m.noteCursor()
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheel.Mouse().Button {
		case tea.MouseWheelUp:
			m.moveBoard(-1)
		case tea.MouseWheelDown:
			m.moveBoard(1)
		}
		return nil
	}
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || m.width < boardWide {
		return nil
	}
	mouse := click.Mouse()
	width := m.width / columns
	col := min(mouse.X/width, columns-1)
	// Under the header and the top of the column, a card in its frame.
	row := (mouse.Y - 2) / cardFrame
	if mouse.Y < 2 || row < 0 {
		return nil
	}
	row += m.board.tops[col]
	if row >= len(m.boardColumns()[col]) {
		return nil
	}
	if col == m.board.col && row == m.board.row {
		return m.boardKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	m.board.col, m.board.row = col, row
	return nil
}

// editCard opens a card of the backlog in the user's editor, or a new one
// when it is nil.
func (m *Model) editCard(c *session.Card) tea.Cmd {
	var card session.Card
	if c != nil {
		card = *c
	}
	f, err := os.CreateTemp("", "cicoria-card-*.md")
	if err != nil {
		m.say("could not write the card: " + err.Error())
		return nil
	}
	_, err = f.WriteString(session.CardText(card))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		m.say("could not write the card: " + err.Error())
		return nil
	}
	id, path := card.ID, f.Name()
	return tea.ExecProcess(m.editor(path), func(err error) tea.Msg { return cardEditedMsg{id, path, err} })
}

// cardEdited takes the card written in the editor into the backlog.
func (m *Model) cardEdited(msg cardEditedMsg) {
	data, err := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	if msg.err != nil {
		m.say("editor: " + msg.err.Error())
		return
	}
	if err != nil {
		m.say("could not read the card: " + err.Error())
		return
	}
	title, prompt, ok := session.ParseCard(string(data))
	i := slices.IndexFunc(m.cards, func(c session.Card) bool { return c.ID == msg.id })
	switch {
	case !ok && msg.id == "":
		m.say("the card was empty: nothing was added")
		return
	case !ok:
		m.say("the card was left empty: it is kept as it was")
		return
	case msg.id == "":
		m.cards = append(m.cards, session.Card{ID: session.NewID(), Title: title, Prompt: prompt, Dir: m.project()})
		i = len(m.cards) - 1
	case i < 0:
		// It was started or deleted while it was written.
		m.say("the card is gone: what was written is lost")
		return
	default:
		m.cards[i].Title, m.cards[i].Prompt = title, prompt
	}
	m.board.col, m.board.row = 0, i
	m.save()
}

// dropCard takes a card out of the backlog.
func (m *Model) dropCard(id string) {
	m.cards = slices.DeleteFunc(m.cards, func(c session.Card) bool { return c.ID == id })
}

// startCard starts the session a card of the backlog is for, as a new
// session is started, asking for its agent: its agent gets the card's
// prompt as the first message.
func (m *Model) startCard(c session.Card) tea.Cmd {
	var agents []agent.Adapter
	for _, a := range m.adapters {
		if !isShell(a) {
			agents = append(agents, a)
		}
	}
	switch len(agents) {
	case 0:
		m.say("no agent is configured to start the card with")
		return nil
	case 1:
		return m.askDir(agents[0], &c)
	}
	var items []ui.Item
	for _, a := range agents {
		items = append(items, ui.Item{Label: a.Name(), Value: cardAgent{a, c}})
	}
	m.openPalette("agent for "+c.Title+" › ", items)
	return nil
}

var notName = regexp.MustCompile(`[^a-z0-9]+`)

// cardName returns a free name for the session of a card, made from its
// title.
func (m *Model) cardName(a agent.Adapter, title string, free func(name string) bool) string {
	base := strings.Trim(notName.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(base) > 24 {
		base = strings.TrimRight(base[:24], "-")
	}
	if base == "" {
		return m.newName(a, free)
	}
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		taken := free != nil && !free(name)
		for _, s := range m.sessions {
			taken = taken || s.Name == name
		}
		if !taken {
			return name
		}
	}
}

// boardHint names the keys for the card under the cursor.
func (m *Model) boardHint() string {
	it := m.boardItem()
	switch {
	case it.card != nil:
		return "enter start · e edit · x delete · J/K move · c new card · q back"
	case it.s != nil:
		return "enter go · space peek · m queue · d changes · r restart · x close · c new card · q back"
	}
	return "h/l column · c new card · q back"
}

// renderBoard draws the board over the whole screen but the status bar.
func (m *Model) renderBoard() []string {
	m.followCursor()
	height := max(m.height-1, 2)
	cols := m.boardColumns()
	waiting := len(cols[1]) + len(cols[3])
	header := fmt.Sprintf(" board  %d card(s) · %d session(s)", len(m.cards), len(m.sessions))
	if waiting > 0 {
		header += fmt.Sprintf(" · %d wait for you", waiting)
	}
	header = ui.Reverse.Render(ui.Fit(header, m.width))
	var rows []string
	if m.width >= boardWide {
		rows = m.renderColumns(cols, height-1)
	} else {
		rows = m.renderList(cols, height-1)
	}
	rows = append([]string{header}, rows...)
	if it := m.boardItem(); m.board.peek && it.s != nil {
		box := m.renderPeek(it.s, height)
		rows = ui.Overlay(rows, box, (m.width-ansi.StringWidth(box[0]))/2, max((height-len(box))/2, 0))
	}
	return rows
}

// renderColumns draws the columns of the board side by side.
func (m *Model) renderColumns(cols [columns][]boardEntry, height int) []string {
	width := m.width / columns
	fits := max((height-2)/cardFrame, 1)
	var out [][]string
	for c, col := range cols {
		w := width
		if c == columns-1 {
			w = m.width - width*(columns-1)
		}
		focused := c == m.board.col
		cursor := -1
		if focused {
			cursor = m.board.row
		}
		top := ui.Window(max(cursor, 0), m.board.tops[c], fits, len(col))
		if !focused {
			top = min(m.board.tops[c], max(len(col)-fits, 0))
		}
		m.board.tops[c] = top
		var lines []string
		// Each card in its frame, lit under the cursor.
		for i := top; i < len(col) && i < top+fits; i++ {
			lines = append(lines, ui.Box("", m.renderCard(col[i], w-4, i == cursor), w-2, cardFrame, i == cursor)...)
		}
		if len(col) == 0 && c == 0 {
			lines = append(lines, ui.Dim.Render(" c writes a card"))
		}
		title := fmt.Sprintf("%s (%d)", columnTitles[c], len(col))
		if top+fits < len(col) {
			title += fmt.Sprintf(" +%d", len(col)-top-fits)
		}
		out = append(out, ui.Box(title, lines, w, height, focused))
	}
	return ui.JoinColumns(out...)
}

// renderList draws the columns of a narrow board one under the other, each
// card in two rows.
func (m *Model) renderList(cols [columns][]boardEntry, height int) []string {
	var lines []string
	at := 0
	for c, col := range cols {
		if len(col) == 0 && c != 0 {
			continue
		}
		head := fmt.Sprintf("── %s (%d) ", columnTitles[c], len(col))
		lines = append(lines, ui.Dim.Render(head+strings.Repeat("─", max(m.width-2-ansi.StringWidth(head), 0))))
		for i, e := range col {
			card := m.renderCard(e, m.width-2, c == m.board.col && i == m.board.row)
			if c == m.board.col && i == m.board.row {
				at = len(lines)
			}
			// The rest of the card, on one row.
			var rest []string
			for _, row := range card[1:] {
				if row = strings.TrimSpace(ansi.Strip(row)); row != "" {
					rest = append(rest, row)
				}
			}
			lines = append(lines, card[0], ui.Dim.Render(ui.Fit("   "+strings.Join(rest, " · "), m.width-2)))
		}
	}
	// The two rows of the card under the cursor are in sight, as far as
	// there is room.
	rows := max(height-2, 1)
	top := ui.Window(at+1, m.board.tops[0], rows, len(lines))
	top = min(ui.Window(at, top, rows, len(lines)), len(lines))
	m.board.tops[0] = top
	return ui.Box("", lines[top:], m.width, height, true)
}

// renderCard draws a card in width cells: its rows, the first one marked
// when it is under the cursor.
func (m *Model) renderCard(e boardEntry, width int, selected bool) []string {
	var rows []string
	if e.card != nil {
		rows = []string{"□ " + ui.Bold.Render(e.card.Title), ui.Dim.Render(tilde(e.card.Dir))}
		if e.card.Prompt != e.card.Title {
			for _, line := range strings.Split(e.card.Prompt, "\n") {
				if line = strings.TrimSpace(line); line != "" && len(rows) < cardRows {
					rows = append(rows, ui.Dim.Render(line))
				}
			}
		}
	} else {
		rows = m.sessionCard(e.s, width)
	}
	for len(rows) < cardRows {
		rows = append(rows, "")
	}
	for i := range rows {
		rows[i] = ui.Fit(" "+rows[i], width)
	}
	if selected {
		rows[0] = ui.Selected(rows[0], width, true, true)
	}
	return rows
}

// sessionCard is what a card says of a session: its name, status and for
// how long; its agent and branch; how much it changed and what waits for it;
// and the last thing its agent said.
func (m *Model) sessionCard(s *session.Session, width int) []string {
	head := badge(s) + " " + s.Name
	if t := lasting(s); t != "" {
		if s.Stalled(time.Now()) {
			t += "?"
		}
		head = ui.Fit(head, max(width-2-ansi.StringWidth(t), 1)) + ui.Dim.Render(t)
	}

	w := m.board.info[s.ID]
	where := s.Adapter.Name()
	if w.branch != "" {
		where += " · ⎇ " + w.branch
	}
	if s.Worktree != "" {
		where += " · wt"
	}

	var stats []string
	if !isShell(s.Adapter) {
		if w.files > 0 {
			stats = append(stats, plural(w.files, "file"))
		}
		if w.commits > 0 {
			stats = append(stats, plural(w.commits, "commit"))
		}
	}
	if n := len(s.Notes); n > 0 {
		stats = append(stats, ui.Warn.Render(plural(n, "note")))
	}
	if n := len(s.Queue); n > 0 {
		q := fmt.Sprintf("⋯%d", n)
		if s.Paused {
			q += " paused"
		}
		stats = append(stats, q)
	}
	if s.Web {
		stats = append(stats, ui.Info.Render("↗"))
	}
	return []string{head, ui.Dim.Render(where), ui.Dim.Render(strings.Join(stats, " · ")), lastWords(s)}
}

// plural counts things in words.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// lastWords is the line that tells best what a session is about: the start
// of what its agent last said, for the agents that tell, or else the last
// line on its screen.
func lastWords(s *session.Session) string {
	switch {
	case s.Err != nil && s.Term == nil:
		return ui.Bad.Render("could not start: " + s.Err.Error())
	case s.Answer != "":
		for _, line := range strings.Split(s.Answer, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return "› " + line
			}
		}
	case s.Term != nil && !s.Adapter.ReportsStatus():
		lines := strings.Split(ansi.Strip(s.Term.Render()), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if line := strings.TrimSpace(lines[i]); line != "" {
				return ui.Dim.Render("› " + line)
			}
		}
	}
	return ""
}

// renderPeek draws the end of the screen of a session in a box over the
// board.
func (m *Model) renderPeek(s *session.Session, height int) []string {
	w, h := min(m.width-4, 120), min(height-2, 30)
	var lines []string
	if s.Term != nil {
		lines = strings.Split(s.Term.Render(), "\n")
		for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = lines[max(len(lines)-(h-2), 0):]
	} else {
		lines = []string{ui.Dim.Render(" its agent is not running")}
	}
	return ui.Box(s.Name+" · "+s.Adapter.Name()+" · any key closes", lines, w, h, true)
}
