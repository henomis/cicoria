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

package ui

import (
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// State is what a lane of the timeline shows in one cell, from the least
// to the most important: a cell shows the most important state of its time.
type State int

// The states of a lane.
const (
	StateNone State = iota
	StateIdle
	StateWorking
	StateDone
	StateWaiting
	StateFailed
)

// stateGlyphs and stateStyles draw each state. The glyphs alone tell them
// apart, without colour.
var (
	stateGlyphs = [...]rune{' ', '─', '█', '✓', '▲', '✕'}
	stateStyles = [...]*lipgloss.Style{nil, &Dim, &Good, &Good, &Warn, &Bad}
)

// Period is a state a lane was in from From to To. A period whose To is zero
// lasts until now; one whose To is From is a moment, as a turn ending.
type Period struct {
	From, To time.Time
	State    State
}

// Mark is something that happened at a moment, drawn as one glyph above
// the lane.
type Mark struct {
	At    time.Time
	Glyph rune
}

// MarkGlyphs are the glyphs of the marks, the most important first: of
// several marks in one cell, the first of them here is shown.
const MarkGlyphs = "↵mtkxo◆●"

// manyMarks is shown in a cell with marks of more than one kind.
const manyMarks = '+'

// Lane is one session on the timeline.
type Lane struct {
	// Label is its name, and Note what is said under it, such as how long
	// it has waited.
	Label, Note string
	Periods     []Period
	Marks       []Mark
	// Dim draws a lane of a session that is closed.
	Dim bool
}

// Timeline is how the timeline is drawn: the time its first cell starts
// at, how long each cell lasts, and now, after which nothing is drawn.
type Timeline struct {
	From time.Time
	Step time.Duration
	Now  time.Time
	// Width is the width of the whole drawing, and LabelWidth that of the
	// labels on its left.
	Width, LabelWidth int
	// Lane is the lane under the cursor, and Cell the cell of it whose
	// marks are selected, or -1.
	Lane, Cell int
}

// Cells returns the number of cells of time the timeline has.
func (t Timeline) Cells() int { return max(t.Width-t.LabelWidth, 0) }

// CellAt returns the cell a moment falls in, which may be outside of the
// timeline.
func (t Timeline) CellAt(at time.Time) int {
	if t.Step <= 0 {
		return 0
	}
	d := at.Sub(t.From)
	if d < 0 {
		return int(d/t.Step) - 1
	}
	return int(d / t.Step)
}

// CellTime returns when a cell starts.
func (t Timeline) CellTime(cell int) time.Time {
	return t.From.Add(time.Duration(cell) * t.Step)
}

// States returns the state of each cell of a lane.
func (t Timeline) States(spans []Period) []State {
	cells := make([]State, t.Cells())
	last := min(t.CellAt(t.Now), len(cells)-1)
	for _, sp := range spans {
		to := sp.To
		if to.IsZero() {
			to = t.Now
		}
		first, end := max(t.CellAt(sp.From), 0), min(t.CellAt(to), last)
		// A period that ended where a cell starts does not reach into it.
		if !sp.To.IsZero() && to.After(sp.From) && end >= 0 && t.CellTime(end).Equal(to) {
			end--
		}
		for c := first; c <= end; c++ {
			cells[c] = max(cells[c], sp.State)
		}
	}
	return cells
}

// Marks returns the glyph of the marks in each cell of a lane, or 0 where
// it has none.
func (t Timeline) Marks(marks []Mark) []rune {
	cells := make([]rune, t.Cells())
	for _, mk := range marks {
		c := t.CellAt(mk.At)
		if c < 0 || c >= len(cells) {
			continue
		}
		switch cells[c] {
		case 0, mk.Glyph:
			cells[c] = mk.Glyph
		default:
			cells[c] = manyMarks
		}
	}
	return cells
}

// Axis returns the row of times above the lanes.
func (t Timeline) Axis() string {
	n := t.Cells()
	row := []rune(Fit("", t.Width))
	tick := axisTick(t.Step)
	first := t.From.Truncate(tick)
	if first.Before(t.From) {
		first = first.Add(tick)
	}
	format := "15:04"
	if t.Step >= time.Hour {
		format = "Mon 15h"
	}
	for at := first; t.CellAt(at) < n; at = at.Add(tick) {
		c := t.CellAt(at)
		label := []rune("┊" + at.Format(format))
		if c+len(label) > n {
			break
		}
		copy(row[t.LabelWidth+c:], label)
	}
	return Dim.Render(string(row))
}

// axisTick returns how far apart the times of the axis are for cells of
// step: a round time, about every dozen cells.
func axisTick(step time.Duration) time.Duration {
	for _, d := range []time.Duration{
		10 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour,
		2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	} {
		if d >= 12*step {
			return d
		}
	}
	return 24 * time.Hour
}

// Render draws the lanes: two rows for each, its marks above its states.
func (t Timeline) Render(lanes []Lane) []string {
	n := t.Cells()
	now := t.CellAt(t.Now)
	var rows []string
	for i, lane := range lanes {
		cursor := i == t.Lane
		label, note := " "+lane.Label, " "+lane.Note
		if cursor {
			label = "▸" + lane.Label
		}
		labelStyle, noteStyle := lipgloss.NewStyle(), Dim
		switch {
		case lane.Dim:
			labelStyle = Dim
		case cursor:
			labelStyle = Bold
		}

		top, bottom := newCanvas(n, 1), newCanvas(n, 1)
		for c, g := range t.Marks(lane.Marks) {
			st := &Accent
			if lane.Dim {
				st = &Dim
			}
			if cursor && c == t.Cell {
				st = &Reverse
				if g == 0 {
					g = ' '
				}
			}
			if g != 0 {
				top.put(c, 0, g, st)
			}
		}
		for c, s := range t.States(lane.Periods) {
			st := stateStyles[s]
			if lane.Dim && st != nil {
				st = &Dim
			}
			bottom.put(c, 0, stateGlyphs[s], st)
		}
		// Now is where the lanes end.
		if now >= 0 && now+1 < n {
			bottom.put(now+1, 0, '│', &Dim)
		}
		rows = append(rows,
			labelStyle.Render(Fit(label, t.LabelWidth))+top.rows()[0],
			noteStyle.Render(Fit(note, t.LabelWidth))+bottom.rows()[0],
		)
	}
	for i := range rows {
		rows[i] = ansi.Truncate(rows[i], t.Width, "")
	}
	return rows
}
