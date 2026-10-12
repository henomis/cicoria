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

package term

// stringFilter keeps UTF-8 text inside control strings from ending them.
//
// A window title or a hyperlink travels in a control string (OSC, DCS and
// the like), which ends at BEL or ST. The emulator also takes the byte 0x9C,
// the one-byte form of ST, as the end, but in UTF-8 that byte is part of
// characters such as ✳ (E2 9C B3): the string ends halfway and the rest of
// it is printed on the screen. Programs that write UTF-8 never mean ST by
// it, so inside a control string it is replaced.
type stringFilter struct {
	state filterState
	// osc says the string is an OSC, the only one BEL ends.
	osc bool
}

type filterState int

const (
	filterGround filterState = iota
	filterEscape
	filterString
	filterStringEscape
)

// filter rewrites p in place. A control string may span several calls.
func (f *stringFilter) filter(p []byte) {
	for i, b := range p {
		switch f.state {
		case filterGround:
			if b == 0x1b {
				f.state = filterEscape
			}
		case filterEscape, filterStringEscape:
			switch b {
			case ']', 'P', '_', '^', 'X':
				f.state, f.osc = filterString, b == ']'
			case 0x1b:
				f.state = filterEscape
			default:
				// Among the rest is the \ of ST, which ends a string.
				f.state = filterGround
			}
		case filterString:
			switch {
			case b == 0x1b:
				f.state = filterStringEscape
			case b == 0x18, b == 0x1a, b == 0x07 && f.osc:
				f.state = filterGround
			case b == 0x9c:
				p[i] = '?'
			}
		}
	}
}
