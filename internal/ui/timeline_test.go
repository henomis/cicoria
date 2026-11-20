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
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

var tl0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.Local)

func minute(n int) time.Time { return tl0.Add(time.Duration(n) * time.Minute) }

func TestStatesKeepTheMostImportant(t *testing.T) {
	tl := Timeline{From: tl0, Step: 5 * time.Minute, Now: minute(30), Width: 10}
	got := tl.States([]Period{
		{From: minute(0), To: minute(10), State: StateWorking},
		// A wait of a minute is not lost in a cell of five.
		{From: minute(10), To: minute(11), State: StateWaiting},
		{From: minute(11), State: StateIdle},
		{From: minute(20), To: minute(20), State: StateDone},
	})
	want := []State{StateWorking, StateWorking, StateWaiting, StateIdle, StateDone, StateIdle, StateIdle, StateNone, StateNone, StateNone}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("states = %v, want %v", got, want)
		}
	}
}

func TestRenderFitsWidth(t *testing.T) {
	bars := []Bar{
		{Label: "api", Periods: []Period{{From: minute(0), State: StateWorking}}, Selected: true, Span: Period{From: minute(2), To: minute(4)}},
		{Label: "a-very-long-session-name", Dim: true},
	}
	for _, width := range []int{1, 20, 80, 160} {
		tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: width, LabelWidth: min(10, width)}
		rows := append(tl.Render(bars), tl.Axis(minute(3)))
		if len(rows) != 3 {
			t.Fatalf("width %d: %d rows", width, len(rows))
		}
		for i, r := range rows {
			if w := ansi.StringWidth(r); w != width {
				t.Errorf("width %d: row %d is %d wide", width, i, w)
			}
		}
	}
	tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: 80, LabelWidth: 10}
	bar := ansi.Strip(tl.Render(bars)[0])
	if !strings.Contains(bar, "███") || !strings.Contains(bar, "│") {
		t.Errorf("bar = %q", bar)
	}
	axis := ansi.Strip(tl.Axis(minute(3)))
	if !strings.Contains(axis, "┊10:15") || !strings.HasSuffix(axis, "now") {
		t.Errorf("axis = %q", axis)
	}
	if i := strings.Index(axis, "▼"); len([]rune(axis[:i])) != 13 {
		t.Errorf("the mark is not over minute 3: %q", axis)
	}
}

// Every state is a full block, told from the others by its colour alone.
func TestStatesByColour(t *testing.T) {
	seen := map[string]State{}
	for s := StateIdle; s <= StateFailed; s++ {
		mark := StateMark(s)
		if ansi.Strip(mark) != "█" {
			t.Errorf("state %d is drawn %q", s, ansi.Strip(mark))
		}
		if other, ok := seen[mark]; ok {
			t.Errorf("states %d and %d have the same colour", other, s)
		}
		seen[mark] = s
	}
	// A closed session's bar keeps the colours, faint.
	tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: 60, LabelWidth: 10}
	periods := []Period{{From: minute(0), State: StateWaiting}}
	open := tl.Render([]Bar{{Label: "api", Periods: periods}})[0]
	closed := tl.Render([]Bar{{Label: "api", Periods: periods, Dim: true}})[0]
	if ansi.Strip(open) != ansi.Strip(closed) || open == closed {
		t.Errorf("closed bar:\n%q\nopen:\n%q", closed, open)
	}
}

// The bar under the cursor draws the same cells as any other: only its
// label says it is selected. Lit cells would turn a half block upside down.
func TestSelectedBarKeepsItsCells(t *testing.T) {
	tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: 60, LabelWidth: 10}
	bar := Bar{Label: "api", Periods: []Period{{From: minute(0), State: StateWorking}}}
	plain := tl.Render([]Bar{bar})[0]
	bar.Selected, bar.Span = true, Period{From: minute(2), To: minute(9)}
	lit := tl.Render([]Bar{bar})[0]
	if a, b := []rune(ansi.Strip(plain)), []rune(ansi.Strip(lit)); string(a[10:]) != string(b[10:]) {
		t.Errorf("the cells changed:\n%q\n%q", string(a[10:]), string(b[10:]))
	}
	if strings.Contains(lit[strings.Index(lit, "█"):], "\x1b[7") {
		t.Error("the cells of the selected bar are reversed")
	}
	// Its time is marked on the axis instead.
	axis := []rune(ansi.Strip(tl.AxisSpan(minute(2), minute(9))))
	if axis[12] != '▼' || axis[13] != '━' || axis[19] != '━' || axis[20] == '━' {
		t.Errorf("axis = %q", string(axis))
	}
}
