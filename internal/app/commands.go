package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// command is something the user can ask for, by prefix key or by name.
type command struct {
	key  string
	name string
	run  func(m *Model) tea.Cmd
}

// commands lists what the prefix key and the command palette offer.
var commands []command

func init() {
	commands = []command{
		{"c", "New session", (*Model).askNewSession},
		{"n", "Next session", func(m *Model) tea.Cmd { return m.switchTo(m.active + 1) }},
		{"p", "Previous session", func(m *Model) tea.Cmd { return m.switchTo(m.active - 1) }},
		{"a", "Go to the next session that needs input", (*Model).nextAttention},
		{",", "Rename session", (*Model).askRename},
		{"r", "Restart session", (*Model).restart},
		{"x", "Close session", (*Model).askClose},
		{"s", "Focus sessions", func(m *Model) tea.Cmd { return m.setFocus(focusSessions) }},
		{"f", "Focus files", func(m *Model) tea.Cmd { return m.setFocus(focusFiles) }},
		{"g", "Focus changes", func(m *Model) tea.Cmd { return m.setFocus(focusChanges) }},
		{"m", "Queue a message for the agent", (*Model).askQueue},
		{"u", "Focus queue", func(m *Model) tea.Cmd { return m.setFocus(focusQueue) }},
		{"d", "Review changes", (*Model).review},
		{"o", "Open file…", (*Model).askFile},
		{"b", "Show or hide side panels", (*Model).togglePanels},
		{"space", "Command palette", (*Model).askCommand},
		{"?", "Help", func(m *Model) tea.Cmd { m.mode = modeHelp; return nil }},
		{"q", "Quit", (*Model).askQuit},
	}
}

// runPrefix runs the command bound to a key pressed after the prefix.
func (m *Model) runPrefix(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if key == m.cfg.Prefix {
		m.send(k)
		return nil
	}
	if key == " " {
		key = "space"
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < len(m.sessions) {
			return m.switchTo(i)
		}
		return nil
	}
	for _, c := range commands {
		if c.key == key {
			return c.run(m)
		}
	}
	m.say(fmt.Sprintf("%s %s is not a command (%s ? for help)", m.cfg.Prefix, key, m.cfg.Prefix))
	return nil
}

// switchTo puts session i on screen, wrapping around the list. The side
// panels follow it to its project.
func (m *Model) switchTo(i int) tea.Cmd {
	if len(m.sessions) == 0 {
		return nil
	}
	m.active = (i + len(m.sessions)) % len(m.sessions)
	m.sessionCursor = m.active
	m.focus = focusAgent
	m.seen()
	m.save()
	return m.showProject()
}

// showProject points the side panels at the project of the session on
// screen, emptying them until it has been read when it is another one.
func (m *Model) showProject() tea.Cmd {
	if m.project() != m.shown {
		m.shown = m.project()
		m.files, m.changes, m.repo, m.loading = nil, nil, false, true
		m.changeCursor, m.changeTop = 0, 0
	}
	return m.refresh()
}

func (m *Model) nextAttention() tea.Cmd {
	for step := 1; step <= len(m.sessions); step++ {
		i := (m.active + step) % len(m.sessions)
		if m.sessions[i].Status == agent.NeedsInput {
			return m.switchTo(i)
		}
	}
	m.say("no session needs input")
	return nil
}

func (m *Model) setFocus(f focus) tea.Cmd {
	if !m.layout().panels {
		m.hidePanels = false
		m.resize()
		if !m.layout().panels {
			m.say("the terminal is too narrow for the side panels")
			return nil
		}
	}
	if s := m.current(); f == focusQueue && (s == nil || len(s.Queue) == 0) {
		m.say(fmt.Sprintf("the queue is empty — %s m adds a message", m.cfg.Prefix))
		return nil
	}
	m.focus = f
	if f == focusSessions {
		m.sessionCursor = m.active
	}
	return nil
}

func (m *Model) togglePanels() tea.Cmd {
	m.hidePanels = !m.hidePanels
	m.focus = focusAgent
	m.resize()
	return nil
}

// newSession creates a session for an agent working in dir and puts it on
// screen.
func (m *Model) newSession(a agent.Adapter, dir string) tea.Cmd {
	s := session.New(session.NewID(), m.newName(a), dir, a)
	m.sessions = append(m.sessions, s)
	// The session list grew, which can change the size of the panes.
	m.resize()
	return tea.Batch(m.start(s), m.switchTo(len(m.sessions)-1))
}

// askDir asks where a new session for an agent should work, offering the
// project on screen.
func (m *Model) askDir(a agent.Adapter) tea.Cmd {
	base := m.project()
	p := &prompt{
		input:    ui.Input{Prompt: a.Name() + " in directory: "},
		complete: func(text string) string { return completeDir(base, text) },
	}
	p.input.Set(tilde(base))
	p.done = func(text string) tea.Cmd {
		dir, err := resolveDir(base, text)
		if err != nil {
			m.say(err.Error())
			return nil
		}
		return m.newSession(a, dir)
	}
	m.prompt = p
	return nil
}

func (m *Model) askNewSession() tea.Cmd {
	if len(m.adapters) == 1 {
		return m.askDir(m.adapters[0])
	}
	var items []ui.Item
	for _, a := range m.adapters {
		items = append(items, ui.Item{Label: a.Name(), Value: a})
	}
	m.openPalette("agent › ", items)
	return nil
}

func (m *Model) askCommand() tea.Cmd {
	var items []ui.Item
	for _, c := range commands {
		if c.key != "space" {
			items = append(items, ui.Item{Label: c.name, Hint: m.cfg.Prefix + " " + c.key, Value: c})
		}
	}
	m.openPalette("› ", items)
	return nil
}

func (m *Model) askFile() tea.Cmd {
	var items []ui.Item
	for _, f := range m.files {
		items = append(items, ui.Item{Label: f, Value: f})
	}
	m.openPalette("file › ", items)
	return nil
}

func (m *Model) openPalette(prompt string, items []ui.Item) {
	m.palette = ui.NewPalette(prompt, items)
	m.mode = modePalette
}

// choose acts on the palette entry the user picked.
func (m *Model) choose(it ui.Item) tea.Cmd {
	m.mode = modeWorkspace
	m.palette = nil
	switch v := it.Value.(type) {
	case command:
		return v.run(m)
	case agent.Adapter:
		return m.askDir(v)
	case string:
		m.tree().Reveal(v)
		m.openFile(v)
	}
	return nil
}

func (m *Model) askRename() tea.Cmd {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	id := s.ID
	p := &prompt{input: ui.Input{Prompt: "rename session: "}}
	p.input.Set(s.Name)
	p.done = func(name string) tea.Cmd {
		if s, _ := m.byID(id); s != nil && name != "" {
			s.Name = name
			m.save()
		}
		return nil
	}
	m.prompt = p
	return nil
}

func (m *Model) restart() tea.Cmd {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	if s.Status != agent.Exited {
		m.say(s.Name + " is still running")
		return nil
	}
	return m.start(s)
}

func (m *Model) askClose() tea.Cmd {
	i := m.active
	if m.focus == focusSessions {
		i = m.sessionCursor
	}
	if i < 0 || i >= len(m.sessions) {
		m.say(errNoSession.Error())
		return nil
	}
	s := m.sessions[i]
	id := s.ID
	closeIt := func() tea.Cmd {
		if s, i := m.byID(id); s != nil {
			return m.closeSession(i)
		}
		return nil
	}
	if s.Status == agent.Exited && len(s.Queue) == 0 {
		return closeIt()
	}
	question := fmt.Sprintf("close %s and stop its agent?", s.Name)
	if n := len(s.Queue); n > 0 {
		question = fmt.Sprintf("close %s, stop its agent and drop %d queued message(s)?", s.Name, n)
	}
	m.confirm = &confirm{question: question, yes: closeIt}
	return nil
}

func (m *Model) closeSession(i int) tea.Cmd {
	m.sessions[i].Close()
	m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
	if m.active > i || m.active >= len(m.sessions) {
		m.active = max(m.active-1, 0)
	}
	m.sessionCursor = max(min(m.sessionCursor, len(m.sessions)-1), 0)
	m.resize()
	m.seen()
	m.save()
	return m.showProject()
}

func (m *Model) askQuit() tea.Cmd {
	// Only agents that say so themselves count: a status guessed from
	// terminal output would raise the question for a shell printing a prompt.
	working := 0
	for _, s := range m.sessions {
		if s.Status == agent.Working && s.Adapter.ReportsStatus() {
			working++
		}
	}
	if working == 0 {
		return tea.Quit
	}
	m.confirm = &confirm{
		question: fmt.Sprintf("%d session(s) still working will be stopped. quit?", working),
		yes:      func() tea.Cmd { return tea.Quit },
	}
	return nil
}

// review opens the diff of the first changed file.
func (m *Model) review() tea.Cmd {
	if len(m.changes) == 0 {
		if m.repo {
			m.say("no changes to review")
		} else {
			m.say("not a git repository")
		}
		return nil
	}
	m.changeCursor = max(min(m.changeCursor, len(m.changes)-1), 0)
	m.openDiff(m.changes[m.changeCursor])
	return nil
}

// mention types a reference to a file at the prompt of the agent on screen.
func (m *Model) mention(path string) {
	s := m.current()
	if path == "" || s == nil || s.Term == nil || s.Status == agent.Exited {
		return
	}
	s.Term.Type(s.Adapter.Mention(path))
	m.focus = focusAgent
}

// edit opens a file in the user's editor, which takes over the terminal
// until it is closed.
func (m *Model) edit(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	argv := append(strings.Fields(editor), path)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.project()
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorMsg{err} })
}
