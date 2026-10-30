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

// Package app is the root model of the cicoria terminal UI.
package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"
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
	"github.com/henomis/cicoria/internal/web"
)

const (
	// coalesce is how long output is gathered before the screen is redrawn.
	coalesce = 8 * time.Millisecond
	// startupGrace is how long an agent has to run for its end to close its
	// session: one that ends sooner could not start, and says why.
	startupGrace = 3 * time.Second
	// tickEvery is how often time-based state is looked at.
	tickEvery = 500 * time.Millisecond
	// refreshTicks is how many ticks pass between looks at the work tree.
	refreshTicks = 4
	// messageFor is how long a status bar message stays.
	messageFor = 4 * time.Second
	// hintFor is how long the message about other workspaces stays.
	hintFor = 15 * time.Second
)

// focus is the part of the workspace that receives keys.
type focus int

const (
	focusAgent focus = iota
	focusSessions
	focusFiles
	focusChanges
	focusCommits
	focusQueue
)

// mode is what is on top of the workspace, if anything.
type mode int

const (
	modeWorkspace mode = iota
	modeView
	modePalette
	modeHelp
	modeWeb
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg config.Config
	// dir is the directory cicoria was opened on: where new sessions start
	// by default, and what the saved workspace is filed under.
	dir      string
	self     string
	adapters []agent.Adapter
	ipc      *ipc.Server
	dark     bool
	// unlock gives up the mark that says this workspace is in use, and next
	// is the directory of the workspace to open once this one is closed.
	unlock func()
	next   string
	// twice says another cicoria had the workspace open when this one came:
	// its agents are not started before the user says so. declined says the
	// user did not, and the workspace is left as the other one keeps it.
	twice    bool
	declined bool

	sessions []*session.Session
	active   int
	// stopping are the closed sessions whose agents are still ending.
	stopping []*session.Session
	// split is the ID of the session shown in the other pane, when the agent
	// pane is split in two; below says the session with the keys is the
	// lower one.
	split string
	below bool

	width, height int
	started       bool
	blurred       bool
	prefix        bool
	focus         focus
	mode          mode
	hideLeft      bool
	hideRight     bool
	ticks         int

	// Left and right panels. The files and changes are those of the project
	// of the session on screen; shown is the directory they were read from.
	sessionCursor int
	sessionTop    int
	trees         map[string]*ui.Tree
	shown         string
	loading       bool
	files         []string
	// ignored are the files git ignores, listed when showIgnored is set.
	ignored     []string
	showIgnored bool
	changes     []git.Change
	// changesBase is the commit the changes are counted from, or empty when
	// they are what is not committed; commits is how many were made since.
	changesBase string
	commits     int
	// branch is the branch the project is on, or its commit when on none.
	branch       string
	changeCursor int
	changeTop    int
	// log is the commits made since changesBase, the newest first; commit is
	// the one the changes are narrowed to, or empty for all of them.
	log         []git.Commit
	logCursor   int
	logTop      int
	commit      string
	queueCursor int
	repo        bool
	// refreshing says a look at the project is under way, and stale that it
	// began before something it depends on changed.
	refreshing bool
	stale      bool

	// anim is the animation playing, if any; animating says its frames are
	// being asked for.
	anim      *animation
	animating bool

	view    fullView
	palette *ui.Palette
	// paletteFrom and helpFrom are what was on screen when the palette or
	// help was opened.
	paletteFrom, helpFrom mode
	// helpTop is the first line of help on screen.
	helpTop int

	// web is the web cockpit, while it runs; webQR is the code of its link,
	// and webCursor the session under the cursor of its box.
	web       *web.Server
	webQR     []string
	webCursor int

	// One-line interactions in the status bar.
	prompt    *prompt
	confirm   *confirm
	message   string
	messageAt time.Time
}

// prompt is a line of text being asked for in the status bar.
type prompt struct {
	input ui.Input
	// complete, when set, completes the text on tab.
	complete func(string) string
	// done receives the text when the user confirms it.
	done func(string) tea.Cmd
}

// confirm is a yes/no question waiting for an answer.
type confirm struct {
	question string
	yes      func() tea.Cmd
	// no, when set, is what a plain no leads to; esc drops the question,
	// unless always says it must be answered: then any other key is a no.
	no     func() tea.Cmd
	always bool
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
		dir string
		// base is the commit the changes were to be counted from; lost says
		// it is no longer there, and they are counted from the last commit.
		base    string
		lost    bool
		files   []string
		ignored []string
		changes []git.Change
		commits int
		// log is the commits since the session started, from since in both
		// views, and commit the one the changes were narrowed to.
		since  string
		log    []git.Commit
		commit string
		branch string
		repo   bool
	}
	// submitMsg is the time to press enter on a message that was pasted to
	// an agent.
	submitMsg struct {
		id   string
		term *term.Terminal
	}
	// editorMsg reports that the external editor was closed.
	editorMsg struct{ err error }
	// openedMsg reports that a file could not be opened outside cicoria.
	openedMsg struct{ err error }
	// removedMsg reports that the worktree of a closed session was removed,
	// or why it could not be.
	removedMsg struct {
		path string
		err  error
	}
	// stoppedMsg reports that the agent of a closed session has ended.
	stoppedMsg struct{ s *session.Session }
	// worktreeMsg reports why the worktree of a closed session must be
	// kept, or that it can go when kept is "".
	worktreeMsg struct{ path, kept string }
	// closingMsg reports why closing the session would keep its worktree,
	// or "" when it would not.
	closingMsg struct{ id, kept string }
)

// New returns a workspace opened on dir, with the sessions it had last time.
// One that another cicoria has open is asked about before its agents start,
// unless agreed says the user was asked already.
func New(cfg config.Config, dir string, dark, agreed bool) (*Model, error) {
	srv, err := ipc.Listen()
	if err != nil {
		return nil, err
	}
	self, _ := os.Executable()

	m := &Model{
		cfg:         cfg,
		hideLeft:    !cfg.LeftPanel,
		hideRight:   !cfg.RightPanel,
		showIgnored: cfg.IgnoredFiles,
		dir:         dir,
		self:        self,
		adapters:    cfg.Adapters(),
		ipc:         srv,
		dark:        dark,
		trees:       map[string]*ui.Tree{},
		twice:       !agreed && session.InUse(dir),
	}
	m.unlock = session.Lock(dir)

	st, err := session.LoadState(session.StatePath(dir))
	if err != nil {
		m.say("could not read saved sessions: " + err.Error())
	} else if len(st.Sessions) == 0 {
		m.hintWorkspaces()
	}
	for _, saved := range st.Sessions {
		if a := m.adapter(saved.Agent); a != nil {
			sdir := saved.Dir
			if sdir == "" {
				sdir = dir
			}
			s := session.New(saved.ID, saved.Name, sdir, a)
			s.Conversation, s.Base, s.Notes = saved.Conversation, saved.Base, saved.Notes
			s.Worktree = saved.Worktree
			s.Uncommitted = cfg.Changes != config.ChangesSession
			// A queue left from last time waits for a go: opening cicoria
			// must not send messages by itself.
			s.Queue, s.Paused = saved.Queue, saved.Paused || len(saved.Queue) > 0
			m.sessions = append(m.sessions, s)
		}
	}
	if len(m.sessions) == 0 {
		a := m.adapter(cfg.DefaultAgent)
		if a == nil {
			m.unlock()
			_ = srv.Close()
			return nil, fmt.Errorf("default agent %q is not configured", cfg.DefaultAgent)
		}
		s := session.New(session.NewID(), m.newName(a, nil), dir, a)
		s.Base, _ = git.Head(dir)
		s.Uncommitted = cfg.Changes != config.ChangesSession
		m.sessions = append(m.sessions, s)
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

// newName returns an unused session name for an agent. With free, it is
// also one that free accepts.
func (m *Model) newName(a agent.Adapter, free func(name string) bool) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", a.Name(), n)
		taken := free != nil && !free(name)
		for _, s := range m.sessions {
			taken = taken || s.Name == name
		}
		if !taken {
			return name
		}
	}
}

// project returns the directory of the session on screen, which is what the
// file and change panels show.
func (m *Model) project() string {
	if s := m.current(); s != nil {
		return s.Dir
	}
	return m.dir
}

// tree returns the file tree of the project on screen. Each project keeps
// its own, so that open folders survive switching back and forth.
func (m *Model) tree() *ui.Tree {
	dir := m.project()
	if m.trees[dir] == nil {
		m.trees[dir] = ui.NewTree()
	}
	return m.trees[dir]
}

// current returns the session on screen, or nil when there is none.
func (m *Model) current() *session.Session {
	if m.active < 0 || m.active >= len(m.sessions) {
		return nil
	}
	return m.sessions[m.active]
}

// target returns the session a command on one session is for: the one
// under the cursor while the sessions panel has the keys, else the one on
// screen.
func (m *Model) target() *session.Session {
	i := m.active
	if m.focus == focusSessions {
		i = m.sessionCursor
	}
	if i < 0 || i >= len(m.sessions) {
		return nil
	}
	return m.sessions[i]
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

// splitWith returns the session the agent pane is shared with, or nil when
// it is not split.
func (m *Model) splitWith() *session.Session {
	if s, i := m.byID(m.split); s != nil && i != m.active {
		return s
	}
	return nil
}

// other returns the session in the pane that does not have the keys, or nil
// when there is one pane.
func (m *Model) other() *session.Session {
	if m.layout().other.h == 0 {
		return nil
	}
	return m.splitWith()
}

// pane returns the area of the screen the terminal of a session has.
func (m *Model) pane(s *session.Session) rect {
	l := m.layout()
	if l.other.h > 0 && s == m.splitWith() {
		return l.other
	}
	return l.agent
}

// watching reports whether the user is looking at session i.
func (m *Model) watching(i int) bool {
	return (i == m.active || m.sessions[i] == m.other()) && !m.blurred && m.mode == modeWorkspace
}

// say shows a message in the status bar for a while.
func (m *Model) say(msg string) {
	m.message, m.messageAt = msg, time.Now()
}

// save remembers the sessions for the next run.
func (m *Model) save() {
	st := session.State{Dir: m.dir, Active: m.active}
	for _, s := range m.sessions {
		st.Sessions = append(st.Sessions, session.Saved{
			ID: s.ID, Name: s.Name, Agent: s.Adapter.Name(), Dir: s.Dir, Conversation: s.Conversation, Base: s.Base,
			Worktree: s.Worktree, Notes: s.Notes, Queue: s.Queue, Paused: s.Paused,
		})
	}
	if err := session.SaveState(session.StatePath(m.dir), st); err != nil {
		m.say("could not save sessions: " + err.Error())
	}
}

// Close stops every agent and saves the workspace.
func (m *Model) Close() error {
	// What was never opened here is the other cicoria's to save.
	if !m.declined {
		m.save()
	}
	// Each agent is given a moment to end: all of them at once. One of a
	// session closed just before is waited for as well.
	var stopping sync.WaitGroup
	for _, s := range slices.Concat(m.sessions, m.stopping) {
		stopping.Go(s.Close)
	}
	stopping.Wait()
	m.stopWeb()
	m.unlock()
	return m.ipc.Close()
}

// closesOnExit reports whether a session whose agent just ended is closed
// rather than left to be restarted. It is left when closing it would lose
// something: an agent that ended as soon as it started has an error to
// read, a queue has messages, and what is open on top of the workspace may
// be about this session.
func (m *Model) closesOnExit(s *session.Session) bool {
	return m.cfg.CloseOnExit &&
		time.Since(s.Started) >= startupGrace &&
		len(s.Queue) == 0 &&
		m.mode == modeWorkspace && m.prompt == nil && m.confirm == nil
}

// Next returns the directory of the workspace the user asked to open in
// place of this one, if any.
func (m *Model) Next() string { return m.next }

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

// reviewBase returns the commit the changes of the session on screen are
// counted from, or nothing when they are what is not committed.
func (m *Model) reviewBase() string {
	if s := m.current(); s != nil && !s.Uncommitted {
		return s.Base
	}
	return ""
}

// sessionBase returns the commit the session on screen started from, which
// its commits are listed since in either view.
func (m *Model) sessionBase() string {
	if s := m.current(); s != nil {
		return s.Base
	}
	return ""
}

// refreshNow looks at the project on screen again after a change to what is
// shown of it, even when a look is already under way.
func (m *Model) refreshNow() tea.Cmd {
	m.stale = m.refreshing
	return m.refresh()
}

// refresh looks at the project on screen in the background.
func (m *Model) refresh() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	dir, base, since, commit := m.project(), m.reviewBase(), m.sessionBase(), m.commit
	// What an ignored directory holds is only read while it is open.
	ignored, open := m.showIgnored, m.tree().Open()
	return func() tea.Msg {
		msg := refreshMsg{dir: dir, base: base, since: since, commit: commit}
		msg.files, _ = git.Files(dir)
		if ignored {
			msg.ignored = git.Ignored(dir, open)
		}
		if base != "" && !git.Exists(dir, base) {
			base, msg.lost = "", true
		}
		if base != "" {
			msg.commits = git.Commits(dir, base)
		}
		if since != "" && git.Exists(dir, since) {
			msg.log, _ = git.Log(dir, since)
		}
		// A commit that is no longer since base is not narrowed to.
		if commit != "" && !slices.ContainsFunc(msg.log, func(c git.Commit) bool { return c.Hash == commit }) {
			commit = ""
		}
		changes, err := git.Status(dir, base)
		if commit != "" {
			changes, err = git.CommitStatus(dir, commit)
		}
		if err == nil {
			msg.changes, msg.repo = changes, true
			msg.branch = git.Branch(dir)
		}
		return msg
	}
}

// startAll launches the agents of the sessions the workspace opens with.
func (m *Model) startAll() tea.Cmd {
	m.play(ui.Bloom, "cicoria", nil)
	var cmds []tea.Cmd
	for _, s := range m.sessions {
		cmds = append(cmds, m.start(s))
	}
	return tea.Batch(cmds...)
}

// start launches the agent of a session in the agent pane.
func (m *Model) start(s *session.Session) tea.Cmd {
	pane := m.pane(s)
	err := s.Start(agent.LaunchOpts{Self: m.self, Socket: m.ipc.Path()}, pane.w, pane.h)
	if err != nil {
		m.say(err.Error())
		return nil
	}
	return waitOutput(s.ID, s.Term)
}

// resize fits every agent's terminal to its pane.
func (m *Model) resize() {
	for _, s := range m.sessions {
		if s.Term != nil {
			pane := m.pane(s)
			s.Term.Resize(pane.w, pane.h)
		}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	// The queue panel goes away with its last message, and the commits
	// panel with the last commit.
	if s := m.current(); m.focus == focusQueue && (s == nil || len(s.Queue) == 0) {
		m.focus = focusAgent
	}
	if m.focus == focusCommits && !m.showsLog() {
		m.focus = focusAgent
	}
	if m.anim != nil && !m.animating {
		m.animating = true
		cmd = tea.Batch(cmd, frame())
	}
	return m, cmd
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.started {
			m.resize()
			return nil
		}
		m.started = true
		if m.twice {
			// Two of them would run the same agents on the same conversations.
			m.confirm = &confirm{
				question: tilde(m.dir) + " is open in another cicoria, and its agents would run twice. open it here too?",
				yes:      m.startAll,
				no:       func() tea.Cmd { m.declined = true; return tea.Quit },
				always:   true,
			}
			return nil
		}
		return m.startAll()

	case frameMsg:
		if m.anim != nil && time.Since(m.anim.start) < animFor {
			return frame()
		}
		m.animating = false
		return m.stopAnim()

	case tea.FocusMsg:
		m.blurred = false
		m.seen()
		// The work tree was not looked at while the terminal was away.
		return m.refresh()
	case tea.BlurMsg:
		m.blurred = true

	case outputMsg:
		s, i := m.byID(msg.id)
		if s == nil || s.Term != msg.term {
			return nil
		}
		s.NoteOutput(m.watching(i), time.Now())
		return waitOutput(msg.id, msg.term)

	case exitMsg:
		s, i := m.byID(msg.id)
		if s == nil || s.Term != msg.term {
			return nil
		}
		if m.closesOnExit(s) {
			m.say(s.Name + " exited")
			return m.closeSession(i)
		}
		s.Exit(m.watching(i))
		// Over the full-screen view the status of the session is not on
		// screen; in the workspace it is, and a restart said there is over.
		if m.mode != modeWorkspace {
			m.say(s.Name + " exited")
		} else if m.message == "restarting "+s.Name {
			m.message = ""
		}

	case stoppedMsg:
		m.stopping = slices.DeleteFunc(m.stopping, func(s *session.Session) bool { return s == msg.s })
		return m.askRemoveWorktree(msg.s.Worktree)

	case worktreeMsg:
		m.offerRemoveWorktree(msg.path, msg.kept)

	case closingMsg:
		m.askCloseWith(msg.id, msg.kept)

	case submitMsg:
		if s, _ := m.byID(msg.id); s != nil && s.Term == msg.term && s.Status != agent.Exited {
			s.Term.SendKey(uv.Key{Code: uv.KeyEnter})
		}

	case hookMsg:
		cmd := m.handleHook(ipc.Event(msg))
		m.publish()
		return tea.Batch(cmd, waitHook(m.ipc))

	case webMsg:
		// A server that was stopped since is not listened to again.
		if msg.srv != m.web {
			return nil
		}
		return tea.Batch(m.handleWeb(msg.Action), waitWeb(m.web))

	case tickMsg:
		return tea.Batch(m.handleTick(time.Time(msg)), tick())

	case refreshMsg:
		m.refreshing = false
		// A look at a project that is no longer on screen is of no use, and
		// the one that is on screen has not been looked at yet.
		if msg.dir != m.project() || msg.base != m.reviewBase() || msg.since != m.sessionBase() || msg.commit != m.commit || m.stale {
			m.stale = false
			return m.refresh()
		}
		if s := m.current(); msg.lost && s != nil {
			// A history that was rewritten took the starting point with it.
			s.Base = ""
			m.save()
			m.say("the commit " + s.Name + " started from is gone: showing what is not committed")
		}
		m.applyRefresh(msg)

	case openedMsg:
		m.say(msg.err.Error())

	case removedMsg:
		if msg.err != nil {
			m.say(msg.err.Error())
		} else {
			m.say("removed the worktree " + tilde(msg.path))
		}

	case editorMsg:
		if msg.err != nil {
			m.say("editor: " + msg.err.Error())
		}
		if m.mode == modeView {
			m.reloadView()
		}
		return m.refresh()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		if s := m.current(); s != nil && s.Term != nil && m.mode == modeWorkspace && m.focus == focusAgent {
			s.NoteInput(time.Now())
			s.Term.Paste(msg.Content)
		}

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	return nil
}

// seen clears the "something happened" flag of the sessions on screen.
func (m *Model) seen() {
	for i, s := range m.sessions {
		if m.watching(i) {
			s.Unseen = false
		}
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

	before, conversation := s.Status, s.Conversation
	s.Apply(u, m.watching(i), time.Now())
	if s.Conversation != conversation {
		m.save()
	}
	if u.Failed {
		m.say(s.Name + ": the turn ended on an error")
	}

	var cmds []tea.Cmd
	cmds = append(cmds, m.answer(s, u))
	if u.TurnEnded {
		cmds = append(cmds, m.trySend(s))
	}
	if u.File != "" && i == m.active {
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
	m.publish()
	// Nobody sees the panels of a terminal that is not in front: the work
	// tree is looked at again when it comes back.
	if m.ticks%refreshTicks == 0 && !m.blurred {
		return m.refresh()
	}
	return nil
}

func (m *Model) applyRefresh(msg refreshMsg) {
	m.loading = false
	m.files, m.ignored = msg.files, msg.ignored
	m.tree().SetFiles(msg.files, msg.ignored)
	m.repo = msg.repo
	m.changesBase, m.commits, m.branch = msg.base, msg.commits, msg.branch
	if msg.lost {
		m.changesBase = ""
	}
	m.applyLog(msg.log)

	// Keep the cursor on the same file while the list changes around it,
	// unless this is another project altogether.
	var selected string
	if msg.dir != m.shown {
		m.shown, m.changeCursor, m.changeTop = msg.dir, 0, 0
	} else if m.changeCursor < len(m.changes) {
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

// applyLog takes a fresh list of the commits, keeping the cursor on the same
// commit, and lets go of the one the changes were narrowed to when it is gone.
func (m *Model) applyLog(log []git.Commit) {
	var selected string
	if m.logCursor < len(m.log) {
		selected = m.log[m.logCursor].Hash
	}
	m.log = log
	found := false
	for i, c := range m.log {
		if c.Hash == selected {
			m.logCursor = i
		}
		found = found || c.Hash == m.commit
	}
	m.logCursor = max(min(m.logCursor, len(m.log)-1), 0)
	if m.commit != "" && !found {
		m.commit = ""
		m.say("the commit the changes were narrowed to is gone: showing them all")
	}
}

// narrow shows the changes of one commit only, or all of them again when it
// is empty. A commit is part of everything since the session started, so
// that is the view it is shown in.
func (m *Model) narrow(hash string) tea.Cmd {
	if s := m.current(); hash != "" && s != nil {
		s.Uncommitted = false
	}
	m.commit = hash
	m.changeCursor, m.changeTop = 0, 0
	return m.refreshNow()
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
		if argv := notifier(runtime.GOOS, text); argv != nil {
			if path, err := exec.LookPath(argv[0]); err == nil {
				_ = exec.Command(path, argv[1:]...).Run()
			}
		}
		return nil
	}
}

// notifier returns the command line that raises a desktop notification on
// the given system.
func notifier(goos, text string) []string {
	if goos == "darwin" {
		// The text is handed over as an argument, not written into the
		// script, so that nothing in a session's name is run.
		return []string{
			"osascript",
			"-e", "on run argv",
			"-e", `display notification (item 1 of argv) with title "cicoria"`,
			"-e", "end run",
			"--", text,
		}
	}
	return []string{"notify-send", "--app-name=cicoria", "cicoria", text}
}

// send passes a key to the agent on screen.
func (m *Model) send(k tea.KeyPressMsg) {
	s := m.current()
	if s == nil || s.Term == nil || s.Status == agent.Exited {
		return
	}
	if m.scrollKey(s, k.String()) {
		return
	}
	s.NoteInput(time.Now())
	s.Term.SendKey(uv.Key(k))
}

// scrollKey scrolls the agent's screen through what has left it, a page at
// a time, unless the agent scrolls by itself. It reports whether the key
// was used for that; page down at the end is the agent's.
func (m *Model) scrollKey(s *session.Session, key string) bool {
	if s.Term.OwnsScrolling() {
		return false
	}
	page := max(m.layout().agent.h-1, 1)
	switch key {
	case "pgup", "shift+pgup":
		s.Term.Scroll(page)
		return true
	case "pgdown", "shift+pgdown":
		if s.Term.Scrolled() == 0 {
			return false
		}
		s.Term.Scroll(-page)
		return true
	}
	return false
}

// errNoSession is shown when a command needs a session and there is none.
var errNoSession = errors.New("no session")
