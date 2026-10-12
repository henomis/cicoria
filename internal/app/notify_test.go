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
	"slices"
	"strings"
	"testing"
)

func TestNotifier(t *testing.T) {
	// A name can hold anything: it must reach the system as text.
	text := `"; do shell script "x" -- needs input`
	for _, goos := range []string{"linux", "darwin"} {
		argv := notifier(goos, text)
		if argv[len(argv)-1] != text {
			t.Errorf("%s: the text is not the last argument: %q", goos, argv)
		}
		if i := slices.IndexFunc(argv[:len(argv)-1], func(a string) bool { return strings.Contains(a, "shell script") }); i >= 0 {
			t.Errorf("%s: the text is part of argument %d: %q", goos, i, argv)
		}
	}
	if got := notifier("darwin", text)[0]; got != "osascript" {
		t.Errorf("darwin notifies with %q", got)
	}
	if got := notifier("linux", text)[0]; got != "notify-send" {
		t.Errorf("linux notifies with %q", got)
	}
}
