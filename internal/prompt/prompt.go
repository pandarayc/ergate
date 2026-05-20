// Package prompt builds the system prompt with clear section structure
// and an explicit boundary between cacheable (stable) and dynamic content.
package prompt

import (
	"fmt"
	"strings"
)

// Input is the data needed to assemble the full system prompt.
type Input struct {
	// Stable (same for entire session — cacheable prefix)
	Memory []MemoryEntry
	Agent  *MemoryEntry

	// Dynamic (may change between turns — goes after cache boundary)
	Skills     []SkillInfo
	InPlanMode bool

	// Environment
	CWD         string
	CurrentDate string
	Shell       string
	IsGitRepo   bool
}

// MemoryEntry is a named piece of persistent context.
type MemoryEntry struct {
	Name        string
	Description string
	Content     string
}

// SkillInfo is a lightweight skill reference for prompt rendering.
type SkillInfo struct {
	Name        string
	Description string
}

// Build assembles the complete system prompt.
//
// Structure:
//
//	=== Stable (cacheable) ===
//	  Identity
//	  Rules & behavior
//	  Memory entries
//	  Agent instructions (CLAUDE.md)
//	=== Cache boundary ===
//	=== Dynamic ===
//	  Environment context
//	  Available skills
//	  Plan mode (when active)
func Build(in Input) string {
	var parts []string

	// === Stable (cacheable) ===

	parts = append(parts, identitySection())

	if len(in.Memory) > 0 {
		parts = append(parts, memorySection(in.Memory))
	}
	if in.Agent != nil {
		parts = append(parts, agentInstructionsSection(*in.Agent))
	}

	// Cache boundary — separates stable prefix from dynamic suffix.
	// Content blocks before this line should have cache_control set.
	parts = append(parts, cacheBoundarySection())

	// === Dynamic ===

	parts = append(parts, environmentSection(in.CWD, in.CurrentDate, in.Shell, in.IsGitRepo))

	if len(in.Skills) > 0 {
		parts = append(parts, skillsSection(in.Skills))
	}
	if in.InPlanMode {
		parts = append(parts, planModeSection())
	}

	return strings.Join(parts, "\n")
}

// --- stable sections ---

func identitySection() string {
	return `## Identity

You are Ergate, You are a helpful
AI assistant with access to software engineering tools for reading, writing,
searching, and executing code.`
}

func memorySection(entries []MemoryEntry) string {
	var b strings.Builder
	b.WriteString("## Project Memory\n")
	for _, e := range entries {
		title := e.Name
		if e.Description != "" {
			title += " — " + e.Description
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", title, e.Content)
	}
	return b.String()
}

func agentInstructionsSection(entry MemoryEntry) string {
	return fmt.Sprintf("## Project Instructions (%s)\n\n%s", entry.Name, entry.Content)
}

// --- cache boundary ---

func cacheBoundarySection() string {
	return "<!-- CACHE_BOUNDARY: content above is stable and cacheable -->"
}

// --- dynamic sections ---

func environmentSection(cwd, currentDate, shell string, isGit bool) string {
	gitInfo := ""
	if isGit {
		gitInfo = "\n- This is a git repository"
	}
	return fmt.Sprintf(`## Environment

- Working directory: %s
- Date: %s
- Platform: %s%s`, cwd, currentDate, shell, gitInfo)
}

func skillsSection(skills []SkillInfo) string {
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- **%s**: %s\n", s.Name, s.Description)
	}
	b.WriteString("\nUse the Skill tool to load a skill for detailed instructions.")
	return b.String()
}

func planModeSection() string {
	return `## Plan Mode

You are in PLAN MODE. In this mode:
- You may ONLY use read-only tools
- You may NOT use write tools or execute shell commands
- Your goal is to explore the codebase and design an implementation plan
- Produce a clear, structured plan before asking to exit plan mode
- When ready, use the ExitPlanMode tool to request plan approval

Follow the design phase carefully. Do not implement anything yet.`
}

