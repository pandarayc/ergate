package prompt

import (
	"fmt"
	"strings"
)

type Input struct {
	Memory     []MemoryEntry
	Agent      *MemoryEntry
	Skills     []SkillInfo
	InPlanMode bool
	CWD         string
	CurrentDate string
	Shell       string
	IsGitRepo   bool
}

type MemoryEntry struct {
	Name        string
	Description string
	Content     string
}

type SkillInfo struct {
	Name        string
	Description string
}

func Build(in Input) string {
	var parts []string

	parts = append(parts, identitySection())
	parts = append(parts, executionStrategySection())

	if len(in.Memory) > 0 {
		parts = append(parts, memorySection(in.Memory))
	}
	if in.Agent != nil {
		parts = append(parts, agentInstructionsSection(*in.Agent))
	}

	parts = append(parts, cacheBoundarySection())
	parts = append(parts, environmentSection(in.CWD, in.CurrentDate, in.Shell, in.IsGitRepo))

	if len(in.Skills) > 0 {
		parts = append(parts, skillsSection(in.Skills))
	}
	if in.InPlanMode {
		parts = append(parts, planModeSection())
	}

	return strings.Join(parts, "\n")
}

func identitySection() string {
	return "## Identity\n\n" +
		"You are Ergate, You are a helpful\n" +
		"AI assistant with access to software engineering tools for reading, writing,\n" +
		"searching, and executing code."
}

// executionStrategySection provides error recovery patterns and tool usage guidelines.
// Uses string concatenation to avoid backtick-in-raw-string-literal issues.
func executionStrategySection() string {
	return "## Execution Protocol\n\n" +
		"### Phase 1 — ANALYZE (turns 1-5)\n" +
		"- Read all relevant files first. Understand requirements and constraints before ANY action.\n" +
		"- Explore the codebase: Glob for files, Grep for patterns, Read key sources.\n" +
		"- Produce a structured plan using TodoWrite with concrete, verifiable steps.\n" +
		"- Do NOT execute anything yet — no Bash, no Write, no Edit. Just read and plan.\n\n" +
		"### Phase 2 — EXECUTE (turns 6+)\n" +
		"- Follow the plan step by step. For code tasks, WRITE COMPLETE FILES — do not solve with bash one-liners.\n" +
		"- Test after each step: write → compile → fix → recompile. Verify before moving on.\n" +
		"- When a command fails, fix the root cause, not the symptoms.\n" +
		"- Do not repeat the same failing command — change approach after 2 failures.\n" +
		"- If stuck after 3 attempts, re-read the plan and adjust.\n\n" +
		"### Tool Guidelines\n" +
		"- WebSearch/WebFetch are SECONDARY — exhaust local tools (Read, Grep, Glob, man, apt-cache) first.\n" +
		"- If WebSearch/WebFetch return \"Network unavailable\", stop using network tools."
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

func cacheBoundarySection() string {
	return "<!-- CACHE_BOUNDARY: content above is stable and cacheable -->"
}

func environmentSection(cwd, currentDate, shell string, isGit bool) string {
	gitInfo := ""
	if isGit {
		gitInfo = "\n- This is a git repository"
	}
	return fmt.Sprintf("## Environment\n\n"+
		"- Working directory: %s\n"+
		"- Date: %s\n"+
		"- Platform: %s%s", cwd, currentDate, shell, gitInfo)
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
	return "## Plan Mode\n\n" +
		"You are in PLAN MODE. In this mode:\n" +
		"- You may ONLY use read-only tools\n" +
		"- You may NOT use write tools or execute shell commands\n" +
		"- Your goal is to explore the codebase and design an implementation plan\n" +
		"- Produce a clear, structured plan before asking to exit plan mode\n" +
		"- When ready, use the ExitPlanMode tool to request plan approval\n\n" +
		"Follow the design phase carefully. Do not implement anything yet."
}

func BuildStable(in Input) string {
	var parts []string
	parts = append(parts, identitySection())
	parts = append(parts, executionStrategySection())
	if len(in.Memory) > 0 {
		parts = append(parts, memorySection(in.Memory))
	}
	if in.Agent != nil {
		parts = append(parts, agentInstructionsSection(*in.Agent))
	}
	return strings.Join(parts, "\n")
}

func BuildDynamicContext(in Input) string {
	var parts []string
	parts = append(parts, environmentSection(in.CWD, in.CurrentDate, in.Shell, in.IsGitRepo))
	if len(in.Skills) > 0 {
		parts = append(parts, skillsSection(in.Skills))
	}
	if in.InPlanMode {
		parts = append(parts, planModeSection())
	}
	return strings.Join(parts, "\n")
}
