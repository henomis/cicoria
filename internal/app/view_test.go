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
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/henomis/cicoria/internal/ui"
)

// When the file list stopped at the cap, the panel says so below the last
// row, without hiding the row under the cursor.
func TestFilesMore(t *testing.T) {
	m := &Model{trees: map[string]*ui.Tree{}, filesMore: true}
	m.tree().SetFiles([]string{"a.go", "b.go", "c.go"}, nil)
	m.tree().Move(2)

	if got := ansi.Strip(strings.Join(m.renderFiles(rect{w: 30, h: 10}), "\n")); !strings.Contains(got, "… more") || !strings.Contains(got, "c.go") {
		t.Errorf("tall panel:\n%s", got)
	}
	if got := ansi.Strip(strings.Join(m.renderFiles(rect{w: 30, h: 3}), "\n")); !strings.Contains(got, "c.go") {
		t.Errorf("one-row panel hides the cursor row:\n%s", got)
	}
}
