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

	"rsc.io/qr"
)

// quiet is the margin of light modules a scanner needs around a QR code.
const quiet = 4

// paper sets the colours of a code: black on white, from the colours of
// the 256-colour palette that themes leave alone, so that the code reads
// the same on any terminal, light or dark.
const paper = "\x1b[38;5;16;48;5;231m"

// QR draws a QR code of text with half blocks, two modules to a cell, in
// black on white.
func QR(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, err
	}
	size := code.Size + 2*quiet
	// black reports whether the module at x, y is black, counting from the
	// outer edge of the margin.
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}

	var rows []string
	for y := 0; y < size; y += 2 {
		var b strings.Builder
		b.WriteString(paper)
		for x := range size {
			top, bottom := black(x, y), y+1 < size && black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString("\x1b[0m")
		rows = append(rows, b.String())
	}
	return rows, nil
}
