package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// tuiDebug is the debug logger for TUI development.
// Enabled by ERGATE_DEBUG=1 or ERGATE_DEBUG_TUI=1.
var tuiDebug *log.Logger

func init() {
	if os.Getenv("ERGATE_DEBUG") == "1" || os.Getenv("ERGATE_DEBUG_TUI") == "1" {
		path := filepath.Join(os.TempDir(), "ergate_tui_debug.log")
		f, err := os.Create(path)
		if err != nil {
			return
		}
		tuiDebug = log.New(f, "", log.Lmicroseconds)
		fmt.Fprintf(os.Stderr, "[ergate] TUI debug log: %s\n", path)
	}
}

// debugf logs a formatted message if debug mode is enabled.
func debugf(format string, args ...any) {
	if tuiDebug == nil {
		return
	}
	_, file, line, _ := runtime.Caller(1)
	tuiDebug.Printf("%s:%d %s", filepath.Base(file), line, fmt.Sprintf(format, args...))
}
