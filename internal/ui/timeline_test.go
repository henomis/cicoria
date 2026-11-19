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

func TestMarksShowPlusForMany(t *testing.T) {
	tl := Timeline{From: tl0, Step: 5 * time.Minute, Now: minute(30), Width: 6}
	got := tl.Marks([]Mark{
		{At: minute(1), Glyph: 'm'}, {At: minute(2), Glyph: 'm'},
		{At: minute(6), Glyph: 'm'}, {At: minute(7), Glyph: '↵'},
		{At: minute(-1), Glyph: 'k'}, {At: minute(99), Glyph: 'k'},
	})
	if string(got[:2]) != "m+" || got[2] != 0 {
		t.Errorf("marks = %q", string(got))
	}
}

func TestRenderFitsWidth(t *testing.T) {
	lanes := []Lane{
		{Label: "api", Note: "▲ 38m", Periods: []Period{{From: minute(0), State: StateWorking}}, Marks: []Mark{{At: minute(3), Glyph: 'm'}}},
		{Label: "a-very-long-session-name", Dim: true},
	}
	for _, width := range []int{1, 20, 80, 160} {
		tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: width, LabelWidth: min(10, width), Cell: 3}
		rows := tl.Render(lanes)
		if len(rows) != 4 {
			t.Fatalf("width %d: %d rows", width, len(rows))
		}
		for i, r := range rows {
			if w := ansi.StringWidth(r); w != width {
				t.Errorf("width %d: row %d is %d wide", width, i, w)
			}
		}
		if axis := tl.Axis(); ansi.StringWidth(axis) != width {
			t.Errorf("width %d: axis is %d wide", width, ansi.StringWidth(axis))
		}
	}
	tl := Timeline{From: tl0, Step: time.Minute, Now: minute(40), Width: 80, LabelWidth: 10}
	bar := ansi.Strip(tl.Render(lanes)[1])
	if !strings.Contains(bar, "███") || !strings.Contains(bar, "│") {
		t.Errorf("bar = %q", bar)
	}
	if axis := ansi.Strip(tl.Axis()); !strings.Contains(axis, "┊10:15") {
		t.Errorf("axis = %q", axis)
	}
}
