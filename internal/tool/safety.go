package tool

import "strings"

// safetyVersion is embedded in the binary to verify which safety.go version is active.
const safetyVersion = "SAFETY_V2_2026-07-01_file_op_rejection"

// SAFETY_V2: 2026-07-01 — file-op command rejection active


// dangerous patterns that are blocked in shell commands.
var dangerousPatterns = []string{
	"rm -rf /",
	"rm -rf --no-preserve-root /",
	"dd if=/dev/zero",
	"mkfs.",
	"> /dev/sda",
	"fork bomb",
	":(){ :|:& };:",
	"chmod 777 /",
	"chown -R root /",
}

// blocklisted commands that require explicit user approval.
var blocklistedCommands = []string{
	"sudo ",
	"su ",
	"passwd",
	"shutdown",
	"reboot",
	"halt",
	"poweroff",
	"iptables",
	"ufw ",
	"mount ",
	"umount ",
	"fdisk ",
	"parted ",
}

// fileReadCommands are shell commands whose sole purpose is reading file
// content. They should be rejected in favor of the Read tool.
// We do NOT reject these when used in pipelines (e.g. "cat file | jq ...").
var fileReadCommands = []string{
	"cat ", "head ", "tail ",
}

// fileWriteCommands are shell patterns that write to files and should be
// rejected in favor of Write/Edit tools.
var fileWritePrefixes = []string{
	"echo ", "sed ", "awk ",
	"cat ", "head ", "tail ",
	"grep ",
}

// IsShellSafe checks if a command contains dangerous patterns
// or uses shell builtins for file operations that should use dedicated tools.
func IsShellSafe(cmd string) (bool, string) {
	lower := strings.ToLower(cmd)

	for _, pattern := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return false, "blocked dangerous pattern: " + pattern
		}
	}

	for _, blocked := range blocklistedCommands {
		if strings.HasPrefix(lower, blocked) || strings.Contains(lower, " "+blocked) || strings.Contains(lower, ";"+blocked) || strings.Contains(lower, "&&"+blocked) || strings.Contains(lower, "|"+blocked) {
			return false, "blocked command: " + blocked
		}
	}

	// Reject direct file reads via cat <file> (no pipeline).
	// "cat file.go" → reject. "cat file.go | jq ..." → allow.
	for _, prefix := range fileReadCommands {
		if strings.HasPrefix(lower, prefix) && !strings.Contains(cmd, "|") {
			return false, "Use the Read tool to read files, not `" + strings.TrimSpace(prefix) + "`"
		}
	}

	// Reject shell writes to files via > or >> redirect.
	if strings.Contains(cmd, ">") {
		for _, prefix := range fileWritePrefixes {
			if strings.HasPrefix(lower, prefix) {
				return false, "Use Write/Edit tools to modify files, not shell redirects"
			}
		}
	}

	return true, ""
}
