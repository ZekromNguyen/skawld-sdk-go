package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Key represents a keyboard input event.
type Key struct {
	Rune     rune
	KeyCode  KeyCode
	Ctrl     bool
	Alt      bool
	Shift    bool
	Raw      string
}

// KeyCode enumerates non-character keyboard keys.
type KeyCode int

const (
	KeyNone       KeyCode = iota
	KeyEnter              // Enter / Return
	KeyTab                // Tab
	KeyEscape             // Escape
	KeyBackspace          // Backspace
	KeyDelete             // Delete
	KeyUp                 // Arrow up
	KeyDown               // Arrow down
	KeyLeft               // Arrow left
	KeyRight              // Arrow right
	KeyHome               // Home
	KeyEnd                // End
	KeyPageUp             // Page Up
	KeyPageDown           // Page Down
	KeyF1                 // F1
	KeyF2                 // F2
	KeyF3                 // F3
	KeyF4                 // F4
	KeyF5                 // F5
	KeyF6                 // F6
	KeyF7                 // F7
	KeyF8                 // F8
	KeyF9                 // F9
	KeyF10                // F10
	KeyF11                // F11
	KeyF12                // F12
	KeyCtrlA              // Ctrl+A
	KeyCtrlC              // Ctrl+C
	KeyCtrlD              // Ctrl+D
	KeyCtrlE              // Ctrl+E
	KeyCtrlK              // Ctrl+K
	KeyCtrlL              // Ctrl+L
	KeyCtrlP              // Ctrl+P
	KeyCtrlR              // Ctrl+R
	KeyCtrlU              // Ctrl+U
	KeyCtrlW              // Ctrl+W
)

// KeyEvent converts the raw key event to a KeyCode.
func (k Key) KeyEvent() string {
	switch {
	case k.KeyCode == KeyEnter:
		return "enter"
	case k.KeyCode == KeyTab:
		return "tab"
	case k.KeyCode == KeyEscape:
		return "esc"
	case k.KeyCode == KeyUp:
		return "up"
	case k.KeyCode == KeyDown:
		return "down"
	case k.KeyCode == KeyCtrlC:
		return "ctrl+c"
	case k.KeyCode == KeyCtrlD:
		return "ctrl+d"
	case k.KeyCode == KeyCtrlE:
		return "ctrl+e"
	case k.KeyCode == KeyCtrlL:
		return "ctrl+l"
	case k.KeyCode == KeyCtrlP:
		return "ctrl+p"
	case k.KeyCode == KeyCtrlR:
		return "ctrl+r"
	default:
		return fmt.Sprintf("%c", k.Rune)
	}
}

// InputReader reads keyboard input from stdin in raw mode.
type InputReader struct {
	reader *bufio.Reader
}

// NewInputReader creates an input reader for stdin.
func NewInputReader() *InputReader {
	return &InputReader{
		reader: bufio.NewReader(os.Stdin),
	}
}

// ReadKey reads a single key event. Blocks until input is available.
func (r *InputReader) ReadKey() (Key, error) {
	b, err := r.reader.ReadByte()
	if err != nil {
		return Key{}, err
	}

	key := Key{Raw: string(b)}

	// Escape sequences
	if b == 27 { // ESC
		// Check if there's more data available
		peek, err := r.reader.Peek(1)
		if err != nil || len(peek) == 0 {
			key.KeyCode = KeyEscape
			return key, nil
		}
		if peek[0] == '[' {
			// CSI sequence — consume the '['
			r.reader.Discard(1)
			seq, err := r.readCSI()
			if err != nil {
				key.KeyCode = KeyEscape
				return key, nil
			}
			key.Raw += "[" + seq
			key.KeyCode = parseCSI(seq, &key)
			return key, nil
		}
		key.KeyCode = KeyEscape
		return key, nil
	}

	// Ctrl sequences
	if b < 32 && b != '\n' && b != '\t' && b != '\r' {
		key.Ctrl = true
		switch b {
		case 1:
			key.KeyCode = KeyCtrlA
		case 3:
			key.KeyCode = KeyCtrlC
		case 4:
			key.KeyCode = KeyCtrlD
		case 5:
			key.KeyCode = KeyCtrlE
		case 11:
			key.KeyCode = KeyCtrlK
		case 12:
			key.KeyCode = KeyCtrlL
		case 16:
			key.KeyCode = KeyCtrlP
		case 18:
			key.KeyCode = KeyCtrlR
		case 21:
			key.KeyCode = KeyCtrlU
		case 23:
			key.KeyCode = KeyCtrlW
		default:
			key.Rune = rune(b + 96)
		}
		return key, nil
	}

	// Regular characters
	switch b {
	case '\n', '\r':
		key.KeyCode = KeyEnter
	case '\t':
		key.KeyCode = KeyTab
	case 127:
		key.KeyCode = KeyBackspace
	default:
		key.Rune = rune(b)
	}

	return key, nil
}

func (r *InputReader) readCSI() (string, error) {
	var seq strings.Builder
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return seq.String(), err
		}
		seq.WriteByte(b)
		// CSI sequences end with a character in range 0x40–0x7E
		if b >= 0x40 && b <= 0x7E {
			return seq.String(), nil
		}
	}
}

func parseCSI(seq string, key *Key) KeyCode {
	if len(seq) == 0 {
		return KeyNone
	}
	switch seq[len(seq)-1] {
	case 'A':
		return KeyUp
	case 'B':
		return KeyDown
	case 'C':
		return KeyRight
	case 'D':
		return KeyLeft
	case 'H':
		return KeyHome
	case 'F':
		return KeyEnd
	case '~':
		// Function keys, Delete, Home, End via ~
		switch seq {
		case "3~":
			return KeyDelete
		case "1~", "7~":
			return KeyHome
		case "4~", "8~":
			return KeyEnd
		case "5~":
			return KeyPageUp
		case "6~":
			return KeyPageDown
		case "11~":
			return KeyF1
		case "12~":
			return KeyF2
		case "13~":
			return KeyF3
		case "14~":
			return KeyF4
		case "15~":
			return KeyF5
		case "17~":
			return KeyF6
		case "18~":
			return KeyF7
		case "19~":
			return KeyF8
		case "20~":
			return KeyF9
		case "21~":
			return KeyF10
		case "23~":
			return KeyF11
		case "24~":
			return KeyF12
		}
	}
	return KeyNone
}

// ─── Line Editor ────────────────────────────────────────────────────────

// LineEditor provides a readline-style line input with history, multi-line
// support, and vi/emacs-inspired keybindings.
type LineEditor struct {
	Prompt          string
	History         []string
	historyPos      int    // current position in history (-1 = new input)
	pos             int
	buf             []rune
	screen          *Screen
	done            bool
	value           string
	multiLine       bool   // true when accepting multi-line input
	savedInput      []rune // saved input when navigating history
	maxHistory      int    // max history entries to keep (0 = unlimited)
	PaletteTriggered bool  // set when Ctrl+P is pressed during editing
}

// NewLineEditor creates a line editor with the given prompt.
func NewLineEditor(prompt string) *LineEditor {
	return &LineEditor{
		Prompt:     prompt,
		historyPos: -1,
		maxHistory: 1000,
	}
}

// ReadLine reads a line of input interactively with full line-editing support.
//
// Keybindings:
//
//	Enter          — submit input
//	Ctrl+J         — insert newline (multi-line mode)
//	Backspace      — delete char before cursor
//	Delete         — delete char at cursor
//	Left/Right     — move cursor
//	Home/End       — start/end of line
//	Ctrl+A / Ctrl+E— start/end of line (emacs)
//	Ctrl+W         — delete word backward
//	Ctrl+U         — delete from cursor to start
//	Ctrl+K         — delete from cursor to end
//	Up/Down        — navigate history
//	Tab            — autocomplete (not yet implemented)
func (l *LineEditor) ReadLine(input *InputReader, screen *Screen) (string, error) {
	l.screen = screen
	l.buf = nil
	l.pos = 0
	l.done = false
	l.historyPos = -1
	l.multiLine = false

	l.redraw()

	for !l.done {
		key, err := input.ReadKey()
		if err != nil {
			if err == io.EOF {
				return "", err
			}
			continue
		}

		switch {
		case key.KeyCode == KeyEnter:
			l.done = true

		case key.KeyCode == KeyCtrlP:
			l.PaletteTriggered = true
			l.done = true

		case key.KeyCode == KeyUp:
			l.navigateHistory(-1)
		case key.KeyCode == KeyDown:
			l.navigateHistory(1)

		case key.KeyCode == KeyBackspace || (key.Ctrl && key.Rune == 'h'):
			if l.pos > 0 {
				l.pos--
				l.buf = append(l.buf[:l.pos], l.buf[l.pos+1:]...)
			}

		case key.KeyCode == KeyDelete:
			if l.pos < len(l.buf) {
				l.buf = append(l.buf[:l.pos], l.buf[l.pos+1:]...)
			}

		case key.KeyCode == KeyLeft:
			if l.pos > 0 {
				l.pos--
			}
		case key.KeyCode == KeyRight:
			if l.pos < len(l.buf) {
				l.pos++
			}

		case key.KeyCode == KeyHome || (key.Ctrl && (key.Rune == 'a' || key.Rune == 'A')):
			l.pos = 0
		case key.KeyCode == KeyEnd || (key.Ctrl && (key.Rune == 'e' || key.Rune == 'E')):
			l.pos = len(l.buf)

		case key.KeyCode == KeyCtrlU:
			// Delete from cursor to start
			l.buf = l.buf[l.pos:]
			l.pos = 0

		case key.KeyCode == KeyCtrlW:
			// Delete word backward
			start := l.pos
			for start > 0 && l.buf[start-1] == ' ' {
				start--
			}
			for start > 0 && l.buf[start-1] != ' ' {
				start--
			}
			l.buf = append(l.buf[:start], l.buf[l.pos:]...)
			l.pos = start

		case key.Ctrl && (key.Rune == 'k' || key.Rune == 'K'):
			// Ctrl+K: delete from cursor to end
			if l.pos < len(l.buf) {
				l.buf = l.buf[:l.pos]
			}

		case key.Ctrl && (key.Rune == 'j' || key.Rune == 'J'):
			// Ctrl+J: insert newline for multi-line input
			l.buf = insert(l.buf, l.pos, '\n')
			l.pos++
			l.multiLine = true

		case key.Rune > 0 && !key.Ctrl:
			l.buf = insert(l.buf, l.pos, key.Rune)
			l.pos++
		}

		l.redraw()
	}

	screen.Write("\r\n")

	// Add to history (skip empty and duplicate of last entry)
	text := string(l.buf)
	if text != "" {
		if len(l.History) == 0 || l.History[len(l.History)-1] != text {
			l.History = append(l.History, text)
			if l.maxHistory > 0 && len(l.History) > l.maxHistory {
				l.History = l.History[len(l.History)-l.maxHistory:]
			}
		}
	}

	return text, nil
}

func (l *LineEditor) navigateHistory(direction int) {
	if len(l.History) == 0 {
		return
	}

	// Save current input when first entering history navigation
	if l.historyPos == -1 {
		if direction < 0 {
			// Going up: save current input, start from end
			l.savedInput = make([]rune, len(l.buf))
			copy(l.savedInput, l.buf)
			l.historyPos = len(l.History) - 1
		} else {
			// Going down from new input: stay
			return
		}
	} else {
		l.historyPos += direction
	}

	// Clamp
	if l.historyPos >= len(l.History) {
		l.historyPos = -1
		l.buf = make([]rune, len(l.savedInput))
		copy(l.buf, l.savedInput)
		l.pos = len(l.buf)
		return
	}
	if l.historyPos < 0 {
		l.historyPos = 0
	}

	// Load history entry
	entry := l.History[l.historyPos]
	l.buf = []rune(entry)
	l.pos = len(l.buf)
}

func (l *LineEditor) redraw() {
	if l.screen == nil {
		return
	}

	prompt := l.Prompt
	if l.multiLine {
		prompt = "  " // continuation prompt for multi-line
	}

	line := prompt + string(l.buf)
	cursorCol := len(prompt) + l.pos + 1

	l.screen.Write("\r" + ClearToEndOfLine())
	l.screen.Write(line)
	l.screen.Write(CursorMove(0, cursorCol))
}

func insert(runes []rune, idx int, r rune) []rune {
	if idx < 0 || idx > len(runes) {
		return append(runes, r)
	}
	runes = append(runes, 0)
	copy(runes[idx+1:], runes[idx:])
	runes[idx] = r
	return runes
}