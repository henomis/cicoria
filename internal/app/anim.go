package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/henomis/cicoria/internal/ui"
)

const (
	// frameEvery is the time between two frames of an animation.
	frameEvery = 40 * time.Millisecond
	// animFor is how long an animation plays.
	animFor = time.Second
)

// animation is a scene playing in the agent pane, in place of the agent.
type animation struct {
	scene ui.Scene
	// title goes on the border of the pane while the scene plays.
	title string
	start time.Time
	// then, when set, is what to do once the scene is over.
	then tea.Cmd
}

// frameMsg is the time to draw the next frame of an animation.
type frameMsg struct{}

func frame() tea.Cmd {
	return tea.Tick(frameEvery, func(time.Time) tea.Msg { return frameMsg{} })
}

// play shows a scene in the agent pane, in place of the one playing, and
// then does what is asked. With animations off, it does that at once.
func (m *Model) play(scene ui.Scene, title string, then tea.Cmd) tea.Cmd {
	if !m.cfg.Animations || m.mode != modeWorkspace {
		return then
	}
	m.anim = &animation{scene: scene, title: title, start: time.Now(), then: then}
	return nil
}

// stopAnim ends the animation and returns what was to follow it.
func (m *Model) stopAnim() tea.Cmd {
	if m.anim == nil {
		return nil
	}
	then := m.anim.then
	m.anim = nil
	return then
}
