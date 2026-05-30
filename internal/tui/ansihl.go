package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// stripAnsi removes all ANSI escape sequences from a string, returning plain text.
func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip the terminating letter
			}
			i = j
		} else if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == ']' {
			// OSC sequence: skip to ST (\x1b\\) or BEL (\x07)
			j := i + 2
			for j < len(s) && s[j] != '\x07' {
				if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			if j < len(s) && s[j] == '\x07' {
				j++
			}
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// injectBgRange applies a background color to terminal columns [startCol, endCol]
// of an ANSI-styled line. Columns use visual width (CJK = 2 columns).
// endCol=-1 means end of line.
func injectBgRange(line string, bgCode string, startCol, endCol int) string {
	if line == "" || bgCode == "" {
		return line
	}
	const bgReset = "\x1b[49m"

	var b strings.Builder
	b.Grow(len(line) + len(bgCode)*4)
	col := 0
	inSel := false
	ended := false // true once we've exited the selection; prevents re-entry

	i := 0
	for i < len(line) {
		if line[i] == '\x1b' && i+1 < len(line) {
			switch line[i+1] {
			case '[':
				j := i + 2
				for j < len(line) && line[j] != 'm' {
					j++
				}
				if j < len(line) && line[j] == 'm' {
					seq := line[i : j+1]
					b.WriteString(seq)
					if inSel && changesBg(seq) {
						b.WriteString(bgCode)
					}
					i = j + 1
					continue
				}
			case ']':
				j := i + 2
				for j < len(line) && line[j] != '\x07' {
					if line[j] == '\x1b' && j+1 < len(line) && line[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(line) && line[j] == '\x07' {
					j++
				}
				i = j
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		w := runewidth.RuneWidth(r)
		if !inSel && !ended && col+w > startCol {
			b.WriteString(bgCode)
			inSel = true
		}
		b.WriteString(line[i : i+size])
		col += w
		i += size
		if endCol >= 0 && col > endCol && inSel {
			b.WriteString(bgReset)
			inSel = false
			ended = true
		}
	}
	if inSel {
		b.WriteString(bgReset)
	}
	return b.String()
}

// sliceByCol extracts a substring from plain text (no ANSI) between visual
// columns [startCol, endCol). CJK characters count as 2 columns.
// endCol=-1 means end of string.
func sliceByCol(s string, startCol, endCol int) string {
	runes := []rune(s)
	col := 0
	lo := len(runes)
	hi := len(runes)
	found := false
	for i, r := range runes {
		w := runewidth.RuneWidth(r)
		if !found && col+w > startCol {
			lo = i
			found = true
		}
		if endCol >= 0 && col >= endCol {
			hi = i
			break
		}
		col += w
	}
	if lo > hi {
		lo = hi
	}
	return string(runes[lo:hi])
}

// injectBg ensures a background color stays active for the entire line,
// re-injecting it after every SGR sequence that could change the background.
func injectBg(line string, bgCode string) string {
	if line == "" || bgCode == "" {
		return line
	}

	var b strings.Builder
	b.Grow(len(line) + len(bgCode)*10)
	b.WriteString(bgCode) // start with bg active

	i := 0
	for i < len(line) {
		if line[i] == '\x1b' && i+1 < len(line) {
			switch line[i+1] {
			case '[':
				j := i + 2
				for j < len(line) && line[j] != 'm' {
					j++
				}
				if j < len(line) && line[j] == 'm' {
					seq := line[i : j+1]
					b.WriteString(seq)
					if changesBg(seq) {
						b.WriteString(bgCode)
					}
					i = j + 1
					continue
				}
			case ']':
				j := i + 2
				for j < len(line) && line[j] != '\x07' {
					if line[j] == '\x1b' && j+1 < len(line) && line[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				if j < len(line) && line[j] == '\x07' {
					j++
				}
				i = j
				continue
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

// changesBg checks if an SGR sequence could change the background color
// (either by resetting it or by setting a new one). We re-inject the selection
// background after any such sequence to keep the highlight visible.
func changesBg(seq string) bool {
	if seq == "\x1b[0m" || seq == "\x1b[m" {
		return true
	}
	inner := seq[2 : len(seq)-1] // strip \x1b[ and m
	params := strings.Split(inner, ";")
	for i, p := range params {
		switch p {
		case "0", "49":
			// Reset all or reset background.
			return true
		case "40", "41", "42", "43", "44", "45", "46", "47":
			// Standard background colors.
			return true
		case "100", "101", "102", "103", "104", "105", "106", "107":
			// Bright background colors.
			return true
		case "48":
			// Extended background: 48;5;N or 48;2;R;G;B.
			if i+1 < len(params) && (params[i+1] == "5" || params[i+1] == "2") {
				return true
			}
		}
	}
	return false
}
