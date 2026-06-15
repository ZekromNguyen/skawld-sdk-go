package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Key represents a keyboard input event.
type Key struct {
	Rune    rune
	KeyCode KeyCode
	Ctrl    bool
	Alt     bool
	Shift   bool
	Raw     string
}

// KeyCode enumerates non-character keyboard keys.
type KeyCode int

const (
	KeyNone      KeyCode = iota
	KeyEnter             // Enter / Return
	KeyTab               // Tab
	KeyEscape            // Escape
	KeyBackspace         // Backspace
	KeyDelete            // Delete
	KeyUp                // Arrow up
	KeyDown              // Arrow down
	KeyLeft              // Arrow left
	KeyRight             // Arrow right
	KeyHome              // Home
	KeyEnd               // End
	KeyPageUp            // Page Up
	KeyPageDown          // Page Down
	KeyF1                // F1
	KeyF2                // F2
	KeyF3                // F3
	KeyF4                // F4
	KeyF5                // F5
	KeyF6                // F6
	KeyF7                // F7
	KeyF8                // F8
	KeyF9                // F9
	KeyF10               // F10
	KeyF11               // F11
	KeyF12               // F12
	KeyCtrlA             // Ctrl+A
	KeyCtrlC             // Ctrl+C
	KeyCtrlD             // Ctrl+D
	KeyCtrlE             // Ctrl+E
	KeyCtrlK             // Ctrl+K
	KeyCtrlL             // Ctrl+L
	KeyCtrlP             // Ctrl+P
	KeyCtrlR             // Ctrl+R
	KeyCtrlU             // Ctrl+U
	KeyCtrlW             // Ctrl+W
	KeyPaste             // Bracketed paste block
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

			// Check for bracketed paste start: \033[200~
			if seq == "200~" {
				// Read until bracketed paste end: \033[201~
				pasteText, err := r.readBracketedPaste()
				if err != nil {
					key.KeyCode = KeyEscape
					return key, nil
				}
				key.KeyCode = KeyPaste
				key.Raw = pasteText
				return key, nil
			}

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

// readBracketedPaste reads everything until the paste end marker \033[201~
func (r *InputReader) readBracketedPaste() (string, error) {
	var buf strings.Builder
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return buf.String(), err
		}
		if b == 27 { // ESC — check for [201~
			peek, err := r.reader.Peek(4)
			if err == nil && len(peek) >= 4 && string(peek[:4]) == "[201" {
				b2, _ := r.reader.ReadByte()
				b3, _ := r.reader.ReadByte()
				b4, _ := r.reader.ReadByte()
				b5, _ := r.reader.ReadByte()
				if b2 == '[' && b3 == '2' && b4 == '0' && b5 == '1' {
					// Consume the '~'
					r.reader.ReadByte()
					return buf.String(), nil
				}
				// Not the marker we expected, write them back
				buf.WriteByte(27)
				buf.WriteByte(b2)
				buf.WriteByte(b3)
				buf.WriteByte(b4)
				buf.WriteByte(b5)
				continue
			}
		}
		buf.WriteByte(b)
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

// HistorySearch tracks Ctrl+R reverse-i-search state.
type HistorySearch struct {
	active   bool
	query    string
	matchIdx int
	matches  []int // indices into LineEditor.History
}

// LineEditor provides a readline-style line input with history, multi-line
// support, and vi/emacs-inspired keybindings.
type LineEditor struct {
	Prompt           string
	History          []string
	historyPos       int // current position in history (-1 = new input)
	pos              int
	buf              []rune
	screen           *Screen
	done             bool
	value            string
	multiLine        bool   // true when accepting multi-line input
	savedInput       []rune // saved input when navigating history
	maxHistory       int    // max history entries to keep (0 = unlimited)
	PaletteTriggered bool   // set when Ctrl+P is pressed during editing

	// Tab autocomplete state
	autocompleteMode  bool
	autocompleteItems []string
	autocompleteIdx   int

	// Ctrl+R search state
	search HistorySearch
}

// NewLineEditor creates a line editor with the given prompt.
func NewLineEditor(prompt string) *LineEditor {
	return &LineEditor{
		Prompt:     prompt,
		historyPos: -1,
		maxHistory: 1000,
		search:     HistorySearch{active: false},
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
//	Ctrl+R         — reverse-i-search history
//	Tab            — autocomplete (slash commands, file paths)
func (l *LineEditor) ReadLine(input *InputReader, screen *Screen) (string, error) {
	l.screen = screen
	l.buf = nil
	l.pos = 0
	l.done = false
	l.historyPos = -1
	l.multiLine = false
	l.search.active = false
	l.autocompleteMode = false

	l.redraw()

	for !l.done {
		key, err := input.ReadKey()
		if err != nil {
			if err == io.EOF {
				return "", err
			}
			continue
		}

		// Handle bracketed paste
		if key.KeyCode == KeyPaste {
			text := key.Raw
			for _, r := range text {
				if r == '\r' || r == '\n' {
					// Replace newlines with spaces in paste (single-line editor)
					r = ' '
				}
				l.buf = insert(l.buf, l.pos, r)
				l.pos++
			}
			l.redraw()
			continue
		}

		// Handle Ctrl+R search mode
		if l.search.active {
			switch {
			case key.KeyCode == KeyCtrlR:
				// Cycle to next match
				l.searchNextMatch()
				l.redrawSearch()
				continue

			case key.KeyCode == KeyCtrlC || key.KeyCode == KeyEscape:
				// Cancel search, restore original input
				l.search.active = false
				if l.savedInput != nil {
					l.buf = make([]rune, len(l.savedInput))
					copy(l.buf, l.savedInput)
					l.pos = len(l.buf)
				}
				l.redraw()
				continue

			case key.KeyCode == KeyEnter:
				// Accept current match
				l.searchAccept()
				l.done = true
				continue

			case key.KeyCode == KeyBackspace:
				if len(l.search.query) > 0 {
					l.search.query = l.search.query[:len(l.search.query)-1]
					l.searchMatches()
				}
				l.redrawSearch()
				continue

			case key.Rune > 0 && !key.Ctrl:
				l.search.query += string(key.Rune)
				l.searchMatches()
				l.redrawSearch()
				continue

			default:
				// Any other key exits search mode
				l.search.active = false
				// Process the key normally
			}
		}

		// Handle tab autocomplete
		if l.autocompleteMode {
			switch {
			case key.KeyCode == KeyTab:
				// Cycle to next completion
				if len(l.autocompleteItems) > 0 {
					l.autocompleteIdx = (l.autocompleteIdx + 1) % len(l.autocompleteItems)
					l.applyAutocomplete()
				}
				l.redraw()
				continue

			case key.KeyCode == KeyEscape, key.KeyCode == KeyCtrlC:
				// Dismiss autocomplete
				l.autocompleteMode = false
				l.autocompleteItems = nil
				l.redraw()
				// Also re-draw to clear the suggestion line
				continue

			case key.KeyCode == KeyEnter:
				// Accept current suggestion and submit
				if len(l.autocompleteItems) > 0 {
					l.applyAutocomplete()
				}
				l.autocompleteMode = false
				l.autocompleteItems = nil
				l.done = true
				continue

			case key.Rune > 0 && !key.Ctrl:
				// Continue typing — dismiss autocomplete
				l.autocompleteMode = false
				l.autocompleteItems = nil
				// Fall through to normal processing
			}
		}

		switch {
		case key.KeyCode == KeyEnter:
			l.done = true

		case key.KeyCode == KeyCtrlP:
			l.PaletteTriggered = true
			l.done = true

		case key.KeyCode == KeyCtrlR:
			// Enter reverse-i-search mode
			l.search.active = true
			l.search.query = ""
			l.search.matchIdx = 0
			l.search.matches = nil
			// Save current input
			l.savedInput = make([]rune, len(l.buf))
			copy(l.savedInput, l.buf)
			l.redrawSearch()

		case key.KeyCode == KeyTab:
			// Trigger autocomplete
			l.triggerAutocomplete()

		case key.KeyCode == KeyUp:
			if l.autocompleteMode {
				// Navigate autocomplete items
				if l.autocompleteIdx > 0 {
					l.autocompleteIdx--
					l.applyAutocomplete()
				}
			} else {
				l.navigateHistory(-1)
			}
		case key.KeyCode == KeyDown:
			if l.autocompleteMode {
				// Navigate autocomplete items
				if l.autocompleteIdx < len(l.autocompleteItems)-1 {
					l.autocompleteIdx++
					l.applyAutocomplete()
				}
			} else {
				l.navigateHistory(1)
			}

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

// ─── Ctrl+R Search ──────────────────────────────────────────────────────

func (l *LineEditor) searchMatches() {
	l.search.matches = nil
	query := strings.ToLower(l.search.query)
	for i := len(l.History) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(l.History[i]), query) {
			l.search.matches = append(l.search.matches, i)
		}
	}
	l.search.matchIdx = 0
	if len(l.search.matches) > 0 {
		// Show the current match in the buffer
		l.loadSearchMatch()
	}
}

func (l *LineEditor) searchNextMatch() {
	if len(l.search.matches) > 1 {
		l.search.matchIdx = (l.search.matchIdx + 1) % len(l.search.matches)
		l.loadSearchMatch()
	}
}

func (l *LineEditor) loadSearchMatch() {
	if l.search.matchIdx < len(l.search.matches) {
		idx := l.search.matches[l.search.matchIdx]
		if idx >= 0 && idx < len(l.History) {
			l.buf = []rune(l.History[idx])
			l.pos = len(l.buf)
		}
	}
}

func (l *LineEditor) searchAccept() {
	if l.search.matchIdx < len(l.search.matches) {
		l.loadSearchMatch()
	}
	l.search.active = false
}

func (l *LineEditor) redrawSearch() {
	if l.screen == nil {
		return
	}

	if l.search.active {
		prompt := SearchPrompt(l.search.query)
		l.screen.Write(prompt)

		// Show the current match content
		content := string(l.buf)
		l.screen.Write(content)
	} else {
		l.redraw()
	}
}

// ─── Tab Autocomplete ────────────────────────────────────────────────────

// slashCommands is the list of known slash commands for autocomplete.
var slashCommands = []string{
	"/help", "/model", "/sessions", "/memory", "/settings",
	"/clear", "/compact", "/export", "/cost", "/theme",
	"/status", "/quit", "/exit",
}

func (l *LineEditor) triggerAutocomplete() {
	currentWord := l.currentWord()

	if strings.HasPrefix(currentWord, "/") {
		// Slash command completion
		l.autocompleteItems = nil
		lower := strings.ToLower(currentWord)
		for _, cmd := range slashCommands {
			if strings.HasPrefix(strings.ToLower(cmd), lower) {
				l.autocompleteItems = append(l.autocompleteItems, cmd)
			}
		}
	} else {
		// File path completion
		l.autocompleteItems = nil
		matches, err := filepath.Glob(currentWord + "*")
		if err == nil {
			for _, m := range matches {
				info, err := os.Stat(m)
				if err != nil {
					continue
				}
				if info.IsDir() {
					l.autocompleteItems = append(l.autocompleteItems, m+string(os.PathSeparator))
				} else {
					l.autocompleteItems = append(l.autocompleteItems, m)
				}
			}
		}
	}

	if len(l.autocompleteItems) > 0 {
		l.autocompleteMode = true
		l.autocompleteIdx = 0
		l.applyAutocomplete()
	}
}

// currentWord returns the word under or before the cursor.
func (l *LineEditor) currentWord() string {
	text := string(l.buf[:l.pos])
	// Find the last space or start
	idx := strings.LastIndexAny(text, " \t")
	if idx < 0 {
		return text
	}
	return text[idx+1:]
}

func (l *LineEditor) applyAutocomplete() {
	if l.autocompleteIdx >= len(l.autocompleteItems) {
		return
	}

	completion := l.autocompleteItems[l.autocompleteIdx]
	currentWord := l.currentWord()

	// Replace the current word with the completion
	text := string(l.buf[:l.pos])
	prefixLen := len([]rune(text)) - len([]rune(currentWord))
	prefix := ""
	if prefixLen > 0 {
		prefix = text[:prefixLen]
	}

	// Build new buffer: prefix + completion + rest of line after cursor
	newText := prefix + completion + string(l.buf[l.pos:])
	l.buf = []rune(newText)
	l.pos = len([]rune(prefix)) + len([]rune(completion))
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

	// Show autocomplete suggestions below the input line if active
	if l.autocompleteMode && len(l.autocompleteItems) > 0 {
		line += "  " + l.renderAutocompleteMenu()
		cursorCol = len(prompt) + l.pos + 1
	}

	l.screen.Write(line)
	l.screen.Write(CursorMove(0, cursorCol))
}

func (l *LineEditor) renderAutocompleteMenu() string {
	if len(l.autocompleteItems) == 0 {
		return ""
	}

	// Show up to 8 items
	start := l.autocompleteIdx - 4
	if start < 0 {
		start = 0
	}
	end := start + 8
	if end > len(l.autocompleteItems) {
		end = len(l.autocompleteItems)
		start = end - 8
		if start < 0 {
			start = 0
		}
	}

	var items []string
	for i := start; i < end; i++ {
		item := l.autocompleteItems[i]
		if i == l.autocompleteIdx {
			item = "\033[7m" + item + "\033[0m" // Reverse video for selected
		} else {
			item = "\033[2m" + item + "\033[0m" // Dim for others
		}
		items = append(items, item)
	}
	return strings.Join(items, "  ")
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
