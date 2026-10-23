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

import (
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
)

// csiKeys are the keys xterm sends as CSI 1;<modifier> <final> once a
// modifier is held, such as ctrl+end for CSI 1;5 F.
var csiKeys = map[rune]byte{
	uv.KeyUp: 'A', uv.KeyDown: 'B', uv.KeyRight: 'C', uv.KeyLeft: 'D',
	uv.KeyHome: 'H', uv.KeyEnd: 'F',
	uv.KeyF1: 'P', uv.KeyF2: 'Q', uv.KeyF3: 'R', uv.KeyF4: 'S',
}

// tildeKeys are the keys xterm sends as CSI <number>;<modifier> ~ once a
// modifier is held, such as ctrl+delete for CSI 3;5 ~.
var tildeKeys = map[rune]int{
	uv.KeyInsert: 2, uv.KeyDelete: 3, uv.KeyPgUp: 5, uv.KeyPgDown: 6,
	uv.KeyF5: 15, uv.KeyF6: 17, uv.KeyF7: 18, uv.KeyF8: 19,
	uv.KeyF9: 20, uv.KeyF10: 21, uv.KeyF11: 23, uv.KeyF12: 24,
}

// encodeKey returns the bytes for keys the emulator does not encode itself:
// printable text typed with shift or alt, shift+enter, and cursor, editing
// and function keys held with a modifier, which the emulator drops. It
// reports false when the key should be left to the emulator.
func encodeKey(k uv.Key) (string, bool) {
	mod := k.Mod &^ (uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock)

	if mod != 0 && mod&^(uv.ModShift|uv.ModAlt|uv.ModCtrl|uv.ModMeta) == 0 {
		// The xterm modifier parameter: 1, plus 1 for shift, 2 for alt, 4
		// for ctrl and 8 for meta.
		param := 1
		for bit, v := range map[uv.KeyMod]int{uv.ModShift: 1, uv.ModAlt: 2, uv.ModCtrl: 4, uv.ModMeta: 8} {
			if mod&bit != 0 {
				param += v
			}
		}
		if final, ok := csiKeys[k.Code]; ok {
			return fmt.Sprintf("\x1b[1;%d%c", param, final), true
		}
		if n, ok := tildeKeys[k.Code]; ok {
			return fmt.Sprintf("\x1b[%d;%d~", n, param), true
		}
	}

	// Agents read alt+enter as "insert a newline"; shift+enter has no legacy
	// encoding of its own, so it is sent the same way.
	if k.Code == uv.KeyEnter && (mod == uv.ModShift || mod == uv.ModAlt) {
		return "\x1b\r", true
	}

	if k.Text == "" {
		return "", false
	}

	switch mod &^ uv.ModShift {
	case 0:
		return k.Text, true
	case uv.ModAlt:
		return "\x1b" + k.Text, true
	}

	return "", false
}
