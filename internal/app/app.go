// Package app is the root model of the cicoria terminal UI.
package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/ipc"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/term"
	"github.com/henomis/cicoria/internal/ui"
)

const (
	// coalesce is how long output is gathered before the screen is redrawn.
	coalesce = 8 * time.Millisecond
	// tickEvery is how often time-based state is looked at.
	tickEvery = 500 * time.Millisecond
	// refreshTicks is how many ticks pass between looks at the work tree.
	refreshTicks = 4
	// messageFor is how long a status bar message stays.
	messageFor = 4 * time.Second
)

// focus is the part of the workspace that receives keys.
type focus int

const (
	focusAgent focus = iota
	focusSessions
	focusFiles
	focusChanges
)

// mode is what is on top of the workspace, if anything.
type mode int

const (
	modeWorkspace mode = iota
	modeView
	modePalette
	modeHelp
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg      config.Config
	dir      string
	self     string
	adapters []agent.Adapter
	ipc      *ipc.Server
	dark     bool

	sessions []*session.Session
	active   int

	width, height int
	started       bool
	blurred       bool
	prefix        bool
	focus         focus
	mode          mode
	hidePanels    bool
	ticks         int

	// Left and right panels.
	sessionCursor int
	tree          *ui.Tree
	files         []string
	changes       []git.Change
	changeCursor  int
	changeTop     int
	repo          bool
	refreshing    bool

	view    fullView
	palette *ui.Palette

	// One-line interactions in the status bar.
	rename    *ui.Input
	confirm   *confirm
	message   string
	messageAt time.Time
}

// confirm is a yes/no question waiting for an answer.
type confirm struct {
	question string
	yes      func() tea.Cmd
}

// Messages.
type (
	// outputMsg reports that an agent's screen changed.
	outputMsg struct {
		id   string
		term *term.Terminal
	}
	// exitMsg reports that an agent exited.
	exitMsg struct {
		id   string
		term *term.Terminal
	}
	// hookMsg carries an event reported by an agent.
	hookMsg ipc.Event
	// tickMsg marks the passing of time.
	tickMsg time.Time
	// refreshMsg carries a fresh look at the work tree.
	refreshMsg struct {
		files   []string
		changes []git.Change
		repo    bool
	}
	// editorMsg reports that the external editor was closed.
	editorMsg struct{ err error }
)

// New returns a workspace for the project in dir, with the sessions it had
// last time.
func New(cfg config.Config, dir string, dark bool) (*Model, error) {
	srv, err := ipc.Listen()
	if err != nil {
		return nil, err
	}
	self, _ := os.Executable()

	m := &Model{
		cfg:      cfg,
		dir:      dir,
		self:     self,
		adapters: cfg.Adapters(),
		ipc:      srv,
		dark:     dark,
		tree:     ui.NewTree(),
	}

	st, err := session.LoadState(session.StatePath(dir))
	if err != nil {
		m.say("could not read saved sessions: " + err.Error())
	}
	for _, saved := range st.Sessions {
		if a := m.adapter(saved.Agent); a != nil {
			m.sessions = append(m.sessions, session.New(saved.ID, saved.Name, a))
		}
	}
	if len(m.sessions) == 0 {
		a := m.adapter(cfg.DefaultAgent)
		if a == nil {
			_ = srv.Close()
			return nil, fmt.Errorf("default agent %q is not configured", cfg.DefaultAgent)
		}
		m.sessions = append(m.sessions, session.New(session.NewID(), m.newName(a), a))
	}
	m.active = max(min(st.Active, len(m.sessions)-1), 0)
	m.sessionCursor = m.active

	return m, nil
}

// adapter returns the agent with the given name, or nil.
func (m *Model) adapter(name string) agent.Adapter {
	for _, a := range m.adapters {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

// newName returns an unused session name for an agent.
func (m *Model) newName(a agent.Adapter) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", a.Name(), n)
		taken := false
		for _, s := range m.sessions {
			taken = taken || s.Name == name
		}
		if !taken {
			return name
		}
	}
}

// current returns the session on screen, or nil when there is none.
func (m *Model) current() *session.Session {
	if m.active < 0 || m.active >= len(m.sessions) {
		return nil
	}
	return m.sessions[m.active]
}

// byID returns a session and its position.
func (m *Model) byID(id string) (*session.Session, int) {
	for i, s := range m.sessions {
		if s.ID == id {
			return s, i
		}
	}
	return nil, -1
}

// watching reports whether the user is looking at session i.
func (m *Model) watching(i int) bool {
	return i == m.active && !m.blurred && m.mode == modeWorkspace
}

// say shows a message in the status bar for a while.
func (m *Model) say(msg string) {
	m.message, m.messageAt = msg, time.Now()
}

// save remembers the sessions for the next run.
func (m *Model) save() {
	st := session.State{Active: m.active}
	for _, s := range m.sessions {
		st.Sessions = append(st.Sessions, session.Saved{ID: s.ID, Name: s.Name, Agent: s.Adapter.Name()})
	}
	if err := session.SaveState(session.StatePath(m.dir), st); err != nil {
		m.say("could not save sessions: " + err.Error())
	}
}

// Close stops every agent and saves the workspace.
func (m *Model) Close() error {
	m.save()
	for _, s := range m.sessions {
		s.Close()
	}
	return m.ipc.Close()
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(waitHook(m.ipc), tick(), m.refresh())
}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitOutput blocks until an agent's screen changes or the agent exits.
func waitOutput(id string, t *term.Terminal) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-t.Done():
			return exitMsg{id, t}
		case <-t.Updates():
			time.Sleep(coalesce)
			return outputMsg{id, t}
		}
	}
}

// waitHook blocks until an agent reports an event.
func waitHook(srv *ipc.Server) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-srv.Events()
		if !ok {
			return nil
		}
		return hookMsg(ev)
	}
}

// refresh looks at the work tree in the background.
func (m *Model) refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	dir := m.dir
	return func() tea.Msg {
		var msg refreshMsg
		msg.files, _ = git.Files(dir)
		if changes, err := git.Status(dir); err == nil {
			msg.changes, msg.repo = changes, true
		}
		return msg
	}
}

// start launches the agent of a session in the agent pane.
func (m *Model) start(s *session.Session) tea.Cmd {
	pane := m.layout().agent
	err := s.Start(agent.LaunchOpts{Dir: m.dir, Self: m.self, Socket: m.ipc.Path()}, pane.w, pane.h)
	if err != nil {
		m.say(err.Error())
		return nil
	}
	return waitOutput(s.ID, s.Term)
}

// resize fits every agent's terminal to the agent pane.
func (m *Model) resize() {
	pane := m.layout().agent
	for _, s := range m.sessions {
		if s.Term != nil {
			s.Term.Resize(pane.w, pane.h)
		}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.started {
			m.resize()
			return m, nil
		}
		m.started = true
		var cmds []tea.Cmd
		for _, s := range m.sessions {
			cmds = append(cmds, m.start(s))
		}
		return m, tea.Batch(cmds...)

	case tea.FocusMsg:
		m.blurred = false
		m.seen()
	case tea.BlurMsg:
		m.blurred = true

	case outputMsg:
		s, i := m.byID(msg.id)
		if s == nil || s.Term != msg.term {
			return m, nil
		}
		s.NoteOutput(m.watching(i), time.Now())
		return m, waitOutput(msg.id, msg.term)

	case exitMsg:
		if s, i := m.byID(msg.id); s != nil && s.Term == msg.term {
			s.Exit(m.watching(i))
		}

	case hookMsg:
		return m, tea.Batch(m.handleHook(ipc.Event(msg)), waitHook(m.ipc))

	case tickMsg:
		return m, tea.Batch(m.handleTick(time.Time(msg)), tick())

	case refreshMsg:
		m.refreshing = false
		m.applyRefresh(msg)

	case editorMsg:
		if msg.err != nil {
			m.say("editor: " + msg.err.Error())
		}
		if m.mode == modeView {
			m.reloadView()
		}
		return m, m.refresh()

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.PasteMsg:
		if s := m.current(); s != nil && s.Term != nil && m.mode == modeWorkspace && m.focus == focusAgent {
			s.NoteInput(time.Now())
			s.Term.Paste(msg.Content)
		}

	case tea.MouseMsg:
		return m, m.handleMouse(msg)
	}

	return m, nil
}

// seen clears the "something happened" flag of the session on screen.
func (m *Model) seen() {
	if s := m.current(); s != nil && m.watching(m.active) {
		s.Unseen = false
	}
}

func (m *Model) handleHook(ev ipc.Event) tea.Cmd {
	s, i := m.byID(ev.Session)
	if s == nil {
		return nil
	}
	u, ok := s.Adapter.MapEvent(ev)
	if !ok {
		return nil
	}

	before := s.Status
	s.Apply(u, m.dir, m.watching(i), time.Now())

	var cmds []tea.Cmd
	if u.File != "" {
		cmds = append(cmds, m.refresh())
	}
	if s.Status == agent.NeedsInput && before != agent.NeedsInput && !m.watching(i) && m.cfg.Notify {
		cmds = append(cmds, notify(s.Name+" needs input"))
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleTick(now time.Time) tea.Cmd {
	for i, s := range m.sessions {
		s.Tick(m.watching(i), now)
	}
	if m.message != "" && now.Sub(m.messageAt) > messageFor {
		m.message = ""
	}
	m.ticks++
	if m.ticks%refreshTicks == 0 {
		return m.refresh()
	}
	return nil
}

func (m *Model) applyRefresh(msg refreshMsg) {
	m.files = msg.files
	m.tree.SetFiles(msg.files)
	m.repo = msg.repo

	// Keep the cursor on the same file while the list changes around it.
	var selected string
	if m.changeCursor < len(m.changes) {
		selected = m.changes[m.changeCursor].Path
	}
	m.changes = msg.changes
	for i, c := range m.changes {
		if c.Path == selected {
			m.changeCursor = i
		}
	}
	m.changeCursor = max(min(m.changeCursor, len(m.changes)-1), 0)
}

// change returns the change of a file, if it has one.
func (m *Model) change(path string) (git.Change, bool) {
	for _, c := range m.changes {
		if c.Path == path {
			return c, true
		}
	}
	return git.Change{}, false
}

// notify raises a desktop notification, where the system has a way to.
func notify(text string) tea.Cmd {
	return func() tea.Msg {
		if path, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(path, "--app-name=cicoria", "cicoria", text).Run()
		}
		return nil
	}
}

// send passes a key to the agent on screen.
func (m *Model) send(k tea.KeyPressMsg) {
	s := m.current()
	if s == nil || s.Term == nil || s.Status == agent.Exited {
		return
	}
	s.NoteInput(time.Now())
	s.Term.SendKey(uv.Key(k))
}

// errNoSession is shown when a command needs a session and there is none.
var errNoSession = errors.New("no session")
