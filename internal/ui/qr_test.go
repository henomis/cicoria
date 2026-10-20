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
	"testing"

	"rsc.io/qr"
)

// TestQR reads the drawn code back module by module, on both themes.
func TestQR(t *testing.T) {
	const url = "http://192.168.1.20:7777/?t=0123456789abcdef0123456789abcdef"
	code, err := qr.Encode(url, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	size := code.Size + 2*quiet

	for _, dark := range []bool{false, true} {
		rows, err := QR(url, dark)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != (size+1)/2 {
			t.Fatalf("dark=%v: %d rows, want %d", dark, len(rows), (size+1)/2)
		}
		for y := range size {
			cells := []rune(rows[y/2])
			if len(cells) != size {
				t.Fatalf("dark=%v: row %d is %d cells, want %d", dark, y/2, len(cells), size)
			}
			for x := range size {
				c := cells[x]
				drawn := c == '█' || (y%2 == 0 && c == '▀') || (y%2 == 1 && c == '▄')
				mx, my := x-quiet, y-quiet
				black := mx >= 0 && my >= 0 && mx < code.Size && my < code.Size && code.Black(mx, my)
				if want := black != dark; drawn != want {
					t.Fatalf("dark=%v: module %d,%d drawn=%v, want %v", dark, x, y, drawn, want)
				}
			}
		}
	}
}
