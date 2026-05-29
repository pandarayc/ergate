package tui

import (
	"strings"
)

// stripAnsi removes all ANSI escape sequences from a string, returning plain text.
func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip to the end of the SGR/CSI sequence (terminated by a letter)
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

// injectBg ensures a background color stays active for the entire line,
// re-injecting it after every SGR reset sequence. This handles the case
// where lipgloss outputs \x1b[0m between styled segments, which would
// otherwise clear the selection background.
//
// Works by parsing the line for SGR sequences (\x1b[...m) and inserting
// the bgCode after any sequence that resets the background:
//   - \x1b[0m  (full reset)
//   - \x1b[49m (bg-only reset)
//   - Any SGR that doesn't set a bg (48;...) — we inject bg after it
func injectBg(line string, bgCode string) string {
	if line == "" || bgCode == "" {
		return line
	}

	var b strings.Builder
	b.Grow(len(line) + len(bgCode)*10) // pre-allocate
	b.WriteString(bgCode)              // start with bg active

	i := 0
	for i < len(line) {
		// Look for ESC
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '[' {
			// Find the end of the SGR sequence (terminated by 'm')
			j := i + 2
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) && line[j] == 'm' {
				// Write the SGR sequence as-is
				seq := line[i : j+1]
				b.WriteString(seq)
				// If this sequence resets bg, re-inject our bg
				if resetsBg(seq) {
					b.WriteString(bgCode)
				}
				i = j + 1
				continue
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

// resetsBg checks if an SGR sequence clears the background color.
func resetsBg(seq string) bool {
	// \x1b[0m — full reset
	if seq == "\x1b[0m" || seq == "\x1b[m" {
		return true
	}
	// \x1b[49m — bg-only reset
	if seq == "\x1b[49m" {
		return true
	}
	// Any sequence that doesn't explicitly set bg (48;...) is treated as
	// potentially resetting bg if it contains "0" as a standalone param.
	// Parse params to check for "0" (reset) without "48" (set bg).
	inner := seq[2 : len(seq)-1] // strip \x1b[ and m
	params := strings.Split(inner, ";")
	hasBg := false
	hasReset := false
	for i, p := range params {
		if p == "0" {
			hasReset = true
		}
		if p == "48" || (p == "49") {
			hasBg = true
			_ = i
		}
	}
	return hasReset && !hasBg
}
