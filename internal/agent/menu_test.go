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

package agent

import (
	"slices"
	"testing"
)

func TestReadMenu(t *testing.T) {
	tests := []struct {
		name, screen string
		want         Menu
		ok           bool
	}{
		{
			name: "claude permission",
			screen: `● Bash(rm -rf build)
╭──────────────────────────────────────────────────────────╮
│ Bash command                                             │
│                                                          │
│   rm -rf build                                           │
│   Remove the build directory                             │
│                                                          │
│ Do you want to proceed?                                  │
│ ❯ 1. Yes                                                 │
│   2. Yes, and don't ask again for rm commands in /p      │
│   3. No, and tell Claude what to do differently (esc)    │
╰──────────────────────────────────────────────────────────╯
`,
			want: Menu{Prompt: "Do you want to proceed?", Choices: []string{
				"Yes", "Yes, and don't ask again for rm commands in /p", "No, and tell Claude what to do differently",
			}, Selected: 0},
			ok: true,
		},
		{
			name: "codex approval, cursor on the second",
			screen: `  Would you like to run the following command?

  $ go test ./...

    1. Yes, proceed (y)
  › 2. Yes, and don't ask again for this command (a)
    3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel
`,
			want: Menu{Prompt: "$ go test ./...", Choices: []string{
				"Yes, proceed", "Yes, and don't ask again for this command", "No, and tell Codex what to do differently",
			}, Selected: 1},
			ok: true,
		},
		{
			name: "a wrapped label",
			screen: `Allow this edit?
❯ 1. Yes
  2. Yes, and allow all edits in this directory for the rest of
     the session
  3. No
`,
			want: Menu{Prompt: "Allow this edit?", Choices: []string{
				"Yes", "Yes, and allow all edits in this directory for the rest of", "No",
			}, Selected: 0},
			ok: true,
		},
		{
			name: "no cursor drawn",
			screen: `Pick one:
1) red
2) blue
`,
			want: Menu{Prompt: "Pick one:", Choices: []string{"red", "blue"}, Selected: -1},
			ok:   true,
		},
		{
			name: "a list the agent wrote, followed by more text",
			screen: `Here is the plan:
1. Add the endpoint
2. Write a test
I'll start with the endpoint and then
write the test, and finally I will run
the whole suite to make sure nothing
else broke along the way.
>
`,
			ok: false,
		},
		{name: "a lone first item", screen: "❯ 1. Yes\n", ok: false},
		{name: "numbers out of order", screen: "1. a\n3. b\n", ok: false},
		{name: "nothing", screen: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := ReadMenu(tt.screen)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v (%+v)", tt.name, ok, tt.ok, got)
			continue
		}
		if !ok {
			continue
		}
		if got.Prompt != tt.want.Prompt || got.Selected != tt.want.Selected || !slices.Equal(got.Choices, tt.want.Choices) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}
}
