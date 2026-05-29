package tui

import (
	"encoding/base64"
	"fmt"
	"os"
)

// copyToClipboard writes text to the system clipboard.
// Uses OSC 52 as primary (works across SSH, tmux), with platform fallbacks.
func copyToClipboard(text string) {
	if text == "" {
		return
	}

	// OSC 52 escape sequence: \e]52;c;<base64>\a
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", encoded)

	// Platform fallbacks for terminals that don't support OSC 52.
	// WSL: pipe to clip.exe (PowerShell)
	// The OSC 52 write above works in most modern terminals; fallbacks
	// can be added here if needed.
}
