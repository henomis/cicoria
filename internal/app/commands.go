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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/agent"
	"github.com/henomis/cicoria/internal/config"
	"github.com/henomis/cicoria/internal/git"
	"github.com/henomis/cicoria/internal/session"
	"github.com/henomis/cicoria/internal/ui"
)

// command is something the user can ask for, by prefix key or by name.
type command struct {
	key  string
	name string
	run  func(m *Model) tea.Cmd
	// keepsView is true for the commands that run over the full-screen
	// view: those that open on top of it, and those that reply, ask or show
	// something in its place there, which leave it when they go elsewhere.
	keepsView bool
}

// commands lists what the prefix key and the command palette offer.
var commands []command

func init() {
	commands = []command{
		{"c", "New session", (*Model).askNewSession, false},
		{"n", "Next session", func(m *Model) tea.Cmd { return m.switchTo(m.active + 1) }, false},
		{"p", "Previous session", func(m *Model) tea.Cmd { return m.switchTo(m.active - 1) }, false},
		{"a", "Go to the next session that needs input", (*Model).nextAttention, true},
		{",", "Rename session", (*Model).askRename, true},
		{"r", "Restart an exited session", (*Model).restart, true},
		{"x", "Close session", (*Model).askClose, true},
		{"-", "Split: show another session below, or close the split", (*Model).askSplit, false},
		{"tab", "Go to the other pane of the split", (*Model).focusOther, false},
		{"w", "Open another workspace…", (*Model).askWorkspace, false},
		{"v", "Web cockpit: share sessions with a phone or a browser…", (*Model).askWeb, false},
		{"s", "Focus sessions", func(m *Model) tea.Cmd { return m.setFocus(focusSessions) }, false},
		{"f", "Focus files", func(m *Model) tea.Cmd { return m.setFocus(focusFiles) }, false},
		{"g", "Focus changes", func(m *Model) tea.Cmd { return m.setFocus(focusChanges) }, false},
		{"l", "Focus commits", func(m *Model) tea.Cmd { return m.setFocus(focusCommits) }, false},
		{"m", "Queue a message for the agent", (*Model).askQueue, true},
		{"k", "Send a message to another session, or ask it", (*Model).askSession, false},
		{"u", "Focus queue", func(m *Model) tea.Cmd { return m.setFocus(focusQueue) }, false},
		{"d", "Review changes", (*Model).review, true},
		{"t", "Send review notes to the agent", (*Model).askSendNotes, true},
		{"h", "Review from here: count changes from the last commit", (*Model).rebase, false},
		{"/", "Find a file by name…", (*Model).askFile, true},
		{"b", "Show or hide side panels", (*Model).togglePanels, false},
		{"[", "Show or hide the left panels", func(m *Model) tea.Cmd { return m.toggleSide(&m.hideLeft) }, false},
		{"]", "Show or hide the right panels", func(m *Model) tea.Cmd { return m.toggleSide(&m.hideRight) }, false},
		{"space", "Command palette", (*Model).askCommand, true},
		{"?", "Help", func(m *Model) tea.Cmd { m.helpFrom, m.mode, m.helpTop = m.mode, modeHelp, 0; return nil }, true},
		{"q", "Quit", (*Model).askQuit, true},
	}
}

// runPrefix runs the command bound to a key pressed after the prefix.
func (m *Model) runPrefix(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if key == m.cfg.Prefix {
		// The agent that gets the key is shown, whatever the prefix is.
		return m.runCommand(false, func(m *Model) tea.Cmd { m.send(k); return nil })
	}
	if key == " " {
		key = "space"
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < len(m.sessions) {
			return m.runCommand(false, func(m *Model) tea.Cmd { return m.switchTo(i) })
		}
		return nil
	}
	for _, c := range commands {
		if c.key == key {
			return m.runCommand(c.keepsView, c.run)
		}
	}
	// Finding a file was on o until v0.0.6: say where it went for a while.
	if key == "o" {
		m.say(fmt.Sprintf("%s / finds a file now", m.cfg.Prefix))
		return nil
	}
	m.say(fmt.Sprintf("%s %s is not a command (%s ? for help)", m.cfg.Prefix, key, m.cfg.Prefix))
	return nil
}

// runCommand runs a command from the prefix or the palette. Over the
// full-screen view it is for the session on screen, and the view stays when
// the command keeps it; otherwise the workspace comes back first.
func (m *Model) runCommand(keepsView bool, run func(m *Model) tea.Cmd) tea.Cmd {
	if m.mode == modeView {
		// The cursor of the sessions panel is not on screen.
		m.sessionCursor = m.active
		if !keepsView {
			m.closeView()
		}
	}
	return run(m)
}

// closeView goes back from the full-screen view to the workspace.
func (m *Model) closeView() {
	m.mode = modeWorkspace
	m.seen()
}

// switchTo puts session i on screen, wrapping around the list. The side
// panels follow it to its project. With the agent pane split, it takes the
// pane that has the keys, and the keys go to the other pane when it is there.
func (m *Model) switchTo(i int) tea.Cmd {
	if len(m.sessions) == 0 {
		return nil
	}
	i = (i + len(m.sessions)) % len(m.sessions)
	if s := m.other(); s != nil && s == m.sessions[i] {
		return m.focusOther()
	}
	m.active = i
	if m.splitWith() == nil {
		m.split, m.below = "", false
	}
	return m.shownNow()
}

// shownNow follows a change of the session that has the keys.
func (m *Model) shownNow() tea.Cmd {
	m.sessionCursor = m.active
	m.focus = focusAgent
	// The commits are those of the session on screen.
	m.commit, m.log, m.logCursor, m.logTop = "", nil, 0, 0
	// A session that came on screen takes the size of its pane.
	m.resize()
	m.seen()
	m.save()
	return m.showProject()
}

// split is the choice of a session to show in the other pane.
type split struct{ id string }

// askSplit asks which session to show below the one on screen, or goes back
// to one pane.
func (m *Model) askSplit() tea.Cmd {
	if m.splitWith() != nil {
		m.split, m.below = "", false
		m.resize()
		return nil
	}
	var items []ui.Item
	for i, s := range m.sessions {
		if i != m.active {
			items = append(items, ui.Item{Label: s.Name, Hint: s.Adapter.Name(), Value: split{s.ID}})
		}
	}
	if len(items) == 0 {
		m.say("there is no other session")
		return nil
	}
	m.openPalette("show below › ", items)
	return nil
}

// openSplit shows a session below the one on screen.
func (m *Model) openSplit(id string) tea.Cmd {
	m.split, m.below = id, false
	if m.other() == nil {
		m.split = ""
		m.say("the terminal is too short for two sessions")
		return nil
	}
	m.resize()
	m.seen()
	return nil
}

// focusOther gives the keys to the session in the other pane. The panes stay
// where they are.
func (m *Model) focusOther() tea.Cmd {
	s, was := m.other(), m.current()
	if s == nil {
		m.say(fmt.Sprintf("one session on screen — %s - shows another below", m.cfg.Prefix))
		return nil
	}
	_, m.active = m.byID(s.ID)
	m.split, m.below = was.ID, !m.below
	return m.shownNow()
}

// showProject points the side panels at the project of the session on
// screen, emptying them until it has been read when it is another one.
func (m *Model) showProject() tea.Cmd {
	if m.project() != m.shown {
		m.shown = m.project()
		m.files, m.changes, m.repo, m.loading = nil, nil, false, true
		m.branch = ""
		m.changeCursor, m.changeTop = 0, 0
	}
	return m.refresh()
}

func (m *Model) nextAttention() tea.Cmd {
	for step := 1; step <= len(m.sessions); step++ {
		i := (m.active + step) % len(m.sessions)
		if m.sessions[i].Status == agent.NeedsInput {
			if m.mode == modeView {
				m.closeView()
			}
			return m.switchTo(i)
		}
	}
	m.say("no session needs input")
	return nil
}

func (m *Model) setFocus(f focus) tea.Cmd {
	// A panel that is asked for comes back, with its side.
	hide, shown := &m.hideLeft, func() bool { return m.layout().left }
	if f == focusChanges || f == focusCommits || f == focusQueue {
		hide, shown = &m.hideRight, func() bool { return m.layout().right }
	}
	if !shown() {
		before := *hide
		*hide = false
		m.resize()
		if !shown() {
			// Asking for it took the room of the other side: leave things
			// as they were.
			*hide = before
			m.resize()
			m.say("the terminal is too narrow for the side panels")
			return nil
		}
	}
	if s := m.current(); f == focusQueue && (s == nil || len(s.Queue) == 0) {
		m.say(fmt.Sprintf("the queue is empty — %s m adds a message", m.cfg.Prefix))
		return nil
	}
	if s := m.current(); f == focusCommits && !m.showsLog() {
		switch {
		case s == nil:
			m.say(errNoSession.Error())
		case s.Base == "" || !m.repo:
			m.say("this session has no commit to count its changes from")
		default:
			m.say("no commits since " + s.Name + " started")
		}
		return nil
	}
	m.focus = f
	if f == focusSessions {
		m.sessionCursor = m.active
	}
	return nil
}

func (m *Model) togglePanels() tea.Cmd {
	// With only one side on screen, both go.
	hide := !m.hideLeft || !m.hideRight
	m.hideLeft, m.hideRight = hide, hide
	m.focus = focusAgent
	m.resize()
	return nil
}

// toggleSide shows or hides the panels of one side.
func (m *Model) toggleSide(hide *bool) tea.Cmd {
	*hide = !*hide
	m.focus = focusAgent
	m.resize()
	return nil
}

// newSession creates a session of the given name for an agent working in
// dir and puts it on screen. With worktree, the session works in a git
// worktree of its own instead, made for it from the commit dir is on.
func (m *Model) newSession(a agent.Adapter, name, dir string, worktree bool) tea.Cmd {
	made := ""
	if worktree {
		var err error
		if made, dir, err = addWorktree(name, dir); err != nil {
			m.say(err.Error())
			return nil
		}
	}
	// One session to a directory is how the panels tell whose work is whose.
	// A shell does no work of its own.
	for _, other := range m.sessions {
		if isShell(a) || isShell(other.Adapter) {
			continue
		}
		if filepath.Clean(other.Dir) == filepath.Clean(dir) {
			m.say(other.Name + " already works in this directory: their changes will be mixed")
			break
		}
	}
	s := session.New(session.NewID(), name, dir, a)
	s.Worktree = made
	s.Base, _ = git.Head(dir)
	s.Uncommitted = m.cfg.Changes != config.ChangesSession
	m.sessions = append(m.sessions, s)
	// The session list grew, which can change the size of the panes.
	m.resize()
	m.play(ui.Liftoff, "launching "+s.Name, nil)
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
		return m.askWorktree(a, dir)
	}
	m.prompt = p
	return nil
}

// askWorktree offers a new session a git worktree of its own when another
// session works in the work tree of dir, where their changes would be mixed.
// A shell is not offered one: it is there to look at the work of the others.
func (m *Model) askWorktree(a agent.Adapter, dir string) tea.Cmd {
	other := m.sharing(dir)
	// A worktree starts from a commit.
	if _, ok := git.Head(dir); other == nil || !ok || isShell(a) {
		return m.askName(a, dir, false)
	}
	m.confirm = &confirm{
		question: other.Name + " already works in this repository: give this session a worktree of its own?",
		yes:      func() tea.Cmd { return m.askName(a, dir, true) },
		no:       func() tea.Cmd { return m.askName(a, dir, false) },
	}
	return nil
}

// isShell tells the plain shell from an agent: it is a way to run a command
// without leaving cicoria, and not a session that changes the project.
func isShell(a agent.Adapter) bool {
	return a.Name() == config.Shell
}

// sharing returns a session that works in the git work tree of dir, if any.
// A shell is not one.
func (m *Model) sharing(dir string) *session.Session {
	root, ok := git.Root(dir)
	if !ok {
		return nil
	}
	for _, s := range m.sessions {
		if isShell(s.Adapter) {
			continue
		}
		if r, ok := git.Root(s.Dir); ok && r == root {
			return s
		}
	}
	return nil
}

var notSlug = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// slug turns the name of a session into one for a directory and a branch.
func slug(name string) string {
	return notSlug.ReplaceAllString(name, "-")
}

// worktreeOf returns where the worktree of a session of that name goes,
// next to the main work tree of its repository.
func worktreeOf(main, name string) string {
	return filepath.Join(main+".worktrees", slug(name))
}

// worktreeFree reports whether a session of that name can have a worktree
// in the repository of dir: one that was made before may still be there.
func worktreeFree(name, dir string) bool {
	main, ok := git.Main(dir)
	if !ok {
		return true
	}
	_, err := os.Stat(worktreeOf(main, name))
	return err != nil
}

// addWorktree makes a git worktree named after a session, next to the
// repository of dir, on the commit dir is on and on no branch. It returns the top of the
// worktree and the directory in it that stands for dir.
func addWorktree(name, dir string) (top, in string, err error) {
	root, ok := git.Root(dir)
	main, isMain := git.Main(dir)
	if !ok || !isMain {
		return "", "", errors.New("not a git repository")
	}
	top = worktreeOf(main, name)
	if _, err := os.Stat(top); err == nil {
		return "", "", fmt.Errorf("a worktree named %s already exists", slug(name))
	}
	if err := git.AddWorktree(dir, top); err != nil {
		return "", "", err
	}
	// A directory git does not know of is not in the worktree.
	in = top
	if rel, err := filepath.Rel(root, dir); err == nil {
		if st, err := os.Stat(filepath.Join(top, rel)); err == nil && st.IsDir() {
			in = filepath.Join(top, rel)
		}
	}
	return top, in, nil
}

// askRemoveWorktree checks, once the agent of a closed session has ended,
// whether the worktree that was made for it can go. One another session
// works in is kept.
func (m *Model) askRemoveWorktree(path string) tea.Cmd {
	if _, err := os.Stat(path); path == "" || err != nil || m.worksIn(path, nil) {
		return nil
	}
	return func() tea.Msg { return worktreeMsg{path, keepWorktree(path)} }
}

// offerRemoveWorktree asks whether to remove the worktree at path, unless
// it must be kept. It is checked again once the answer is yes, for the
// question may wait a long time.
func (m *Model) offerRemoveWorktree(path, kept string) {
	switch {
	case m.worksIn(path, nil):
		return
	case kept != "":
		m.say("worktree kept at " + tilde(path) + ": " + kept)
		return
	case m.confirm != nil:
		// Another question is not pushed away.
		m.say("worktree kept at " + tilde(path))
		return
	}
	m.confirm = &confirm{
		question: fmt.Sprintf("remove the worktree %s?", tilde(path)),
		yes: func() tea.Cmd {
			return func() tea.Msg {
				if kept := keepWorktree(path); kept != "" {
					return worktreeMsg{path, kept}
				}
				return removedMsg{path, git.RemoveWorktree(path)}
			}
		},
	}
}

// worksIn reports whether a session other than except works in the
// worktree at path.
func (m *Model) worksIn(path string, except *session.Session) bool {
	for _, s := range m.sessions {
		if rel, err := filepath.Rel(path, s.Dir); s != except && err == nil && filepath.IsLocal(rel) {
			return true
		}
	}
	return false
}

// keepWorktree says why the worktree at path must be kept, or "" when it
// can go: when it holds work not committed, commits on no branch, or files
// that removing it would fail to delete.
func keepWorktree(path string) string {
	if !git.Clean(path) {
		return "it has changes that are not committed"
	}
	if !git.OnBranch(path) {
		return "it has commits that are on no branch"
	}
	if f := git.Undeletable(path); f != "" {
		return "it has files you cannot delete, in " + tilde(f)
	}
	return ""
}

// askName asks what to call a new session, offering a name that is free.
// With worktree, the name is also that of the worktree made for it.
func (m *Model) askName(a agent.Adapter, dir string, worktree bool) tea.Cmd {
	// The name of a worktree that is still there is not offered again.
	var free func(string) bool
	if worktree {
		free = func(name string) bool { return worktreeFree(name, dir) }
	}
	p := &prompt{input: ui.Input{Prompt: "session name: "}}
	p.input.Set(m.newName(a, free))
	p.done = func(name string) tea.Cmd {
		if name == "" {
			name = m.newName(a, free)
		}
		return m.newSession(a, name, dir, worktree)
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
	for _, f := range m.ignored {
		if !strings.HasSuffix(f, "/") {
			items = append(items, ui.Item{Label: f, Value: f})
		}
	}
	m.openPalette("file › ", items)
	return nil
}

func (m *Model) openPalette(prompt string, items []ui.Item) {
	m.palette = ui.NewPalette(prompt, items)
	// Leaving it without a choice goes back to where it was opened from.
	// A palette is never opened from another: choose puts back what was
	// under it first.
	m.paletteFrom, m.mode = m.mode, modePalette
}

// choose acts on the palette entry the user picked.
func (m *Model) choose(it ui.Item) tea.Cmd {
	// A command runs as from the prefix, over what the palette was opened on.
	m.mode, m.palette = m.paletteFrom, nil
	if c, ok := it.Value.(command); ok {
		return m.runCommand(c.keepsView, c.run)
	}
	if m.mode == modeView {
		m.closeView()
	}
	switch v := it.Value.(type) {
	case agent.Adapter:
		return m.askDir(v)
	case sendNotes:
		return m.sendReview(v.queue)
	case split:
		return m.openSplit(v.id)
	case ask:
		return m.askReply(v)
	case askHow:
		return m.askMessage(ask(v))
	case workspace:
		return m.askOpen(v)
	case string:
		m.tree().Reveal(v)
		m.showFile(v)
	}
	return nil
}

func (m *Model) askRename() tea.Cmd {
	s := m.target()
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
	s := m.target()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	if s.Status != agent.Exited {
		m.say(s.Name + " is still running")
		return nil
	}
	m.say("restarting " + s.Name)
	return m.start(s)
}

func (m *Model) askClose() tea.Cmd {
	s := m.target()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
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
	// An agent that is still there can be asked to fix a worktree that
	// closing would keep.
	if path := s.Worktree; path != "" && s.Status != agent.Exited && !m.worksIn(path, s) {
		return func() tea.Msg { return closingMsg{id, keepWorktree(path)} }
	}
	m.askCloseWith(id, "")
	return nil
}

// askCloseWith asks whether to close the session. When kept says why its
// worktree would be kept, it asks instead whether its agent should fix that
// first: no closes it all the same, and closing again once the agent is
// done looks at the worktree again.
func (m *Model) askCloseWith(id, kept string) {
	s, _ := m.byID(id)
	if s == nil {
		return
	}
	closeIt := func() tea.Cmd {
		if s, i := m.byID(id); s != nil {
			return m.closeSession(i)
		}
		return nil
	}
	if kept == "" {
		question := fmt.Sprintf("close %s and stop its agent?", s.Name)
		if n := len(s.Queue); n > 0 {
			question = fmt.Sprintf("close %s, stop its agent and drop %d queued message(s)?", s.Name, n)
		}
		m.confirm = &confirm{question: question, yes: closeIt}
		return
	}
	question := fmt.Sprintf("closing %s keeps its worktree: %s. ask its agent to fix that first?", s.Name, kept)
	if n := len(s.Queue); n > 0 {
		question = fmt.Sprintf("closing %s drops %d queued message(s) and keeps its worktree: %s. ask its agent to fix that first?", s.Name, n, kept)
	}
	m.confirm = &confirm{
		question: question,
		yes: func() tea.Cmd {
			s, _ := m.byID(id)
			if s == nil {
				return nil
			}
			m.say("asked " + s.Name + " to fix its worktree: close it again once it is done")
			return m.enqueue(s, session.Message{Text: fixPrompt(s.Worktree, kept)})
		},
		no: closeIt,
	}
}

// fixPrompt asks an agent to make its worktree safe to remove, saying why
// it is not.
func fixPrompt(path, kept string) string {
	return fmt.Sprintf("This session is about to close, and cicoria removes its git worktree %s then. "+
		"It cannot do so now: %s. Make the worktree safe to remove without losing any work, "+
		"for example by committing changes or by putting commits on a branch. "+
		"Do not discard or delete work to get there. "+
		"If you cannot fix it, explain why and tell me what to do.", path, kept)
}

func (m *Model) closeSession(i int) tea.Cmd {
	// The file of a session that goes is not left on screen.
	if i == m.active && m.mode == modeView {
		m.closeView()
	}
	if i == m.active {
		m.play(ui.Burst, m.sessions[i].Name+" closed", nil)
	}
	closed := m.sessions[i]
	m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
	if m.active > i || m.active >= len(m.sessions) {
		m.active = max(m.active-1, 0)
	}
	m.sessionCursor = max(min(m.sessionCursor, len(m.sessions)-1), 0)
	// The session left on screen may be the one the pane was shared with.
	if m.splitWith() == nil {
		m.split, m.below = "", false
	}
	m.resize()
	m.seen()
	m.save()
	// The agent is given a moment to end, which is not for the screen to
	// wait for. Quitting in that moment waits for it. Its worktree is
	// looked at once it has ended, for it may still write there.
	m.stopping = append(m.stopping, closed)
	stop := func() tea.Msg { closed.Close(); return stoppedMsg{closed} }
	return tea.Batch(stop, m.showProject())
}

func (m *Model) askQuit() tea.Cmd {
	return m.askLeave("quit?", "")
}

// askLeave closes the workspace, asking first when that stops agents at
// work. With next, the workspace of that directory is opened in its place.
func (m *Model) askLeave(question, next string) tea.Cmd {
	// Only agents that say so themselves count: a status guessed from
	// terminal output would raise the question for a shell printing a prompt.
	working := 0
	for _, s := range m.sessions {
		if s.Status == agent.Working && s.Adapter.ReportsStatus() {
			working++
		}
	}
	// Any key cuts the goodbye short.
	quit := func() tea.Cmd {
		// The goodbye plays in the workspace.
		if m.mode == modeView {
			m.closeView()
		}
		m.next = next
		return m.play(ui.Wilt, "ciao", tea.Quit)
	}
	if working == 0 {
		return quit()
	}
	m.confirm = &confirm{
		question: fmt.Sprintf("%d session(s) still working will be stopped. %s", working, question),
		yes:      quit,
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

// rebase makes the last commit the one the changes of the session on screen
// are counted from, once what came before has been looked at.
func (m *Model) rebase() tea.Cmd {
	s := m.current()
	if s == nil {
		m.say(errNoSession.Error())
		return nil
	}
	head, ok := git.Head(s.Dir)
	if !ok {
		m.say("no commit to count from")
		return nil
	}
	s.Base = head
	m.commit = ""
	m.save()
	m.say("changes of " + s.Name + " are counted from here")
	return m.refresh()
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

// open hands a file to the program the system opens it with, such as the
// image viewer or the browser, and leaves it running next to cicoria.
func (m *Model) open(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	sys := system()
	if _, err := exec.LookPath(opener(sys)); err != nil && sys == "wsl" {
		// Windows programs cannot be run from here: open it as Linux does.
		sys = "linux"
	}
	program, dir := opener(sys), m.project()
	m.say("opening " + path)
	return func() tea.Msg {
		target := path
		if sys == "wsl" {
			// explorer.exe takes the path as Windows writes it.
			if !filepath.IsAbs(target) {
				target = "./" + target
			}
			convert := exec.Command("wslpath", "-w", target)
			convert.Dir = dir
			out, err := convert.Output()
			if err != nil {
				return openedMsg{fmt.Errorf("wslpath %s: %w", path, err)}
			}
			target = strings.TrimSpace(string(out))
		}
		cmd := exec.Command(program, target)
		cmd.Dir = dir
		err := cmd.Run()
		// explorer.exe fails even when it has opened the file.
		var exit *exec.ExitError
		if sys == "wsl" && errors.As(err, &exit) {
			err = nil
		}
		if err != nil {
			return openedMsg{fmt.Errorf("%s %s: %w", program, path, err)}
		}
		return nil
	}
}
