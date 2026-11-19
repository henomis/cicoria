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
)

// State is what a bar of the timeline shows in one cell, from the least to
// the most important: a cell shows the most important state of its time.
type State int

// The states of a bar.
const (
	StateNone State = iota
	StateIdle
	StateWorking
	StateDone
	StateWaiting
	StateFailed
)

// stateGlyphs and stateStyles draw each state, the more it needs you the
// heavier: waiting is a full block, working half of one. The glyphs alone
// tell them apart, without colour.
var (
	stateGlyphs = [...]rune{' ', '─', '▄', '✓', '█', '✕'}
	failed      = Bad.Reverse(true)
	stateStyles = [...]*lipgloss.Style{nil, &Dim, &Good, nil, &Warn, &failed}
)

// Period is a state a session was in from From to To. A period whose To is
// zero lasts until now; one whose To is From is a moment, as a turn ending.
type Period struct {
	From, To time.Time
	State    State
}

// Bar is one session on the timeline.
type Bar struct {
	// Label is drawn on its left, and Dim draws it all faded, as for a
	// session that is closed.
	Label   string
	Periods []Period
	Dim     bool
	// Selected draws the bar as the one under the cursor, with Span, the
	// time of what is under the cursor, lit on it.
	Selected bool
	Span     Period
}

// Timeline is how the bars are drawn: the time the first cell starts at,
// how long each cell lasts, and now, after which nothing is drawn.
type Timeline struct {
	From time.Time
	Step time.Duration
	Now  time.Time
	// Width is the width of the whole drawing, and LabelWidth that of the
	// labels on its left.
	Width, LabelWidth int
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

// States returns the state of each cell of a bar.
func (t Timeline) States(periods []Period) []State {
	cells := make([]State, t.Cells())
	last := min(t.CellAt(t.Now), len(cells)-1)
	for _, p := range periods {
		to := p.To
		if to.IsZero() {
			to = t.Now
		}
		first, end := max(t.CellAt(p.From), 0), min(t.CellAt(to), last)
		// A period that ended where a cell starts does not reach into it.
		if !p.To.IsZero() && to.After(p.From) && end >= 0 && t.CellTime(end).Equal(to) {
			end--
		}
		for c := first; c <= end; c++ {
			cells[c] = max(cells[c], p.State)
		}
	}
	return cells
}

// Axis returns the row of times above the bars, with a mark over the
// moment at, unless it is zero, and "now" at its end.
func (t Timeline) Axis(at time.Time) string {
	n := t.Cells()
	row := []rune(Fit("", t.Width))
	tick := axisTick(t.Step)
	first := t.From.Truncate(tick)
	if first.Before(t.From) {
		first = first.Add(tick)
	}
	format := "15:04"
	if t.Step*time.Duration(n) > 24*time.Hour {
		format = "Mon 15h"
	}
	free := func(from, to int) bool {
		for x := max(from, 0); x < min(to, len(row)); x++ {
			if row[x] != ' ' {
				return false
			}
		}
		return true
	}
	// Room is kept for "now" at the end.
	for at := first; t.CellAt(at) < n; at = at.Add(tick) {
		c := t.CellAt(at)
		label := []rune("┊" + at.Format(format))
		if c+len(label) > n-4 {
			break
		}
		copy(row[t.LabelWidth+c:], label)
	}
	if n >= 4 && free(t.Width-4, t.Width) {
		copy(row[t.Width-3:], []rune("now"))
	}
	out := Dim.Render(string(row))
	if c := t.CellAt(at); !at.IsZero() && c >= 0 && c < n {
		x := t.LabelWidth + c
		out = Dim.Render(string(row[:x])) + Accent.Render("▼") + Dim.Render(string(row[x+1:]))
	}
	return out
}

// axisTick returns how far apart the times of the axis are for cells of
// step: a round time, about every dozen cells.
func axisTick(step time.Duration) time.Duration {
	for _, d := range []time.Duration{
		time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
		30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	} {
		if d >= 12*step {
			return d
		}
	}
	return 24 * time.Hour
}

// Render draws the bars, one row each.
func (t Timeline) Render(bars []Bar) []string {
	n := t.Cells()
	now := t.CellAt(t.Now)
	rows := make([]string, 0, len(bars))
	for _, bar := range bars {
		lit := func(int) bool { return false }
		if bar.Selected && !bar.Span.From.IsZero() {
			to := bar.Span.To
			if to.IsZero() {
				to = t.Now
			}
			first, last := t.CellAt(bar.Span.From), t.CellAt(to)
			lit = func(x int) bool { return x >= first && x <= last }
		}
		c := newCanvas(n, 1)
		for x, s := range t.States(bar.Periods) {
			st := stateStyles[s]
			if bar.Dim && st != nil {
				st = &Dim
			}
			if lit(x) {
				on := Reverse
				if st != nil {
					on = st.Reverse(true)
				}
				st = &on
			}
			c.put(x, 0, stateGlyphs[s], st)
		}
		// Now is where the bars end.
		if now >= 0 && now+1 < n {
			c.put(now+1, 0, '│', &Dim)
		}
		label := Fit(" "+bar.Label, t.LabelWidth)
		switch {
		case bar.Selected:
			label = Reverse.Render(label)
		case bar.Dim:
			label = Dim.Render(label)
		}
		rows = append(rows, label+c.rows()[0])
	}
	return rows
}
