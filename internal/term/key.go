package term

import uv "github.com/charmbracelet/ultraviolet"

// encodeKey returns the bytes for keys the emulator does not encode itself:
// printable text typed with shift or alt, and shift+enter. It reports false
// when the key should be left to the emulator.
func encodeKey(k uv.Key) (string, bool) {
	mod := k.Mod &^ (uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock)

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
