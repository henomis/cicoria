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
	"fmt"
	"testing"
)

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d of a long text that is cut on the right", i)
	}
	return lines
}

// Updating the text keeps where the user is: the scroll, the cursor and
// the match of a search, which does not jump.
func TestPagerUpdateKeepsThePlace(t *testing.T) {
	var p Pager
	p.SetContent(numbered(30), nil)
	p.Search("line 2")
	p.NextMatch(1)
	p.Top, p.Cursor, p.Left = 12, 20, 5
	match := p.matches[p.match]

	p.Update(numbered(60))
	if p.Top != 12 || p.Cursor != 20 || p.Left != 5 {
		t.Errorf("top %d, cursor %d, left %d", p.Top, p.Cursor, p.Left)
	}
	if p.Len() != 60 || p.matches[p.match] != match {
		t.Errorf("%d lines, match on line %d, want %d", p.Len(), p.matches[p.match], match)
	}

	// Shorter text keeps the cursor on it.
	p.Update(numbered(3))
	if p.Cursor > 2 || p.Top > 2 {
		t.Errorf("top %d, cursor %d of 3 lines", p.Top, p.Cursor)
	}
}
