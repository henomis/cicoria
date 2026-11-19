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
	if !strings.Contains(bar, "▄▄▄") || !strings.Contains(bar, "│") {
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

// Waiting weighs more than working, so that it stands out.
func TestWaitingIsHeaviest(t *testing.T) {
	if stateGlyphs[StateWaiting] != '█' || stateGlyphs[StateWorking] != '▄' {
		t.Errorf("waiting is %q and working %q", stateGlyphs[StateWaiting], stateGlyphs[StateWorking])
	}
}
