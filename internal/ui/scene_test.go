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

	"github.com/charmbracelet/x/ansi"
)

func TestScenesFillTheirPane(t *testing.T) {
	scenes := map[string]Scene{"liftoff": Liftoff, "burst": Burst, "bloom": Bloom, "wilt": Wilt}
	for name, scene := range scenes {
		// Sizes down to nothing: a pane can be smaller than the drawing.
		for _, size := range [][2]int{{80, 30}, {50, 14}, {9, 3}, {1, 1}, {0, 0}} {
			for p := 0.0; p <= 1; p += 0.05 {
				rows := scene(p, size[0], size[1])
				if len(rows) != size[1] {
					t.Fatalf("%s at %.2f in %v: %d rows", name, p, size, len(rows))
				}
				for _, row := range rows {
					if w := ansi.StringWidth(row); w != size[0] {
						t.Fatalf("%s at %.2f in %v: a row is %d cells wide", name, p, size, w)
					}
				}
			}
		}
	}
}

func TestScenesTellTheirStory(t *testing.T) {
	text := func(rows []string) string { return ansi.Strip(strings.Join(rows, "\n")) }

	// The rocket stands on its pad, and is gone in the end.
	if got := text(Liftoff(0, 50, 14)); !strings.Contains(got, "|[]|") || !strings.Contains(got, "/_|__|_\\") {
		t.Errorf("no rocket on the pad:\n%s", got)
	}
	if got := text(Liftoff(1, 50, 14)); strings.Contains(got, "|[]|") {
		t.Errorf("the rocket never left:\n%s", got)
	}

	// The chicory is in flower over the whole name, and goes back to a seed.
	if got := text(Bloom(1, 50, 14)); !strings.Contains(got, "( @ )") || !strings.Contains(got, "c i c o r i a") {
		t.Errorf("no flower in the end:\n%s", got)
	}
	if got := text(Bloom(0, 50, 14)); strings.Contains(got, "@") {
		t.Errorf("in flower from the start:\n%s", got)
	}
	if got := text(Wilt(1, 50, 14)); strings.Contains(got, "@") || !strings.Contains(got, "c i a o !") {
		t.Errorf("still in flower at the end:\n%s", got)
	}

	// The sparks spread out.
	early, late := Burst(0.3, 50, 14), Burst(0.9, 50, 14)
	if strings.TrimSpace(ansi.Strip(early[0])) != "" || strings.TrimSpace(ansi.Strip(late[0])) == "" {
		t.Errorf("the sparks do not reach the edge late:\n%s\n\n%s", text(early), text(late))
	}
}
