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

// executionStrategySection provides the two-phase execution protocol with tool-bound steps.
func executionStrategySection() string {
	return "## Execution Protocol\n\n" +
		"### Phase 1 — ANALYZE (turns 1-5): Understand, then plan\n" +
		"Follow this sequence before executing anything:\n" +
		"1. **Glob** — find relevant source files (e.g. `**/*.go`, `**/*.py`).\n" +
		"2. **Grep** — search for key patterns to understand structure.\n" +
		"3. **Read** — read every file you plan to modify.\n" +
		"4. **TodoWrite** — create a concrete step-by-step plan with verifiable items.\n" +
		"Do NOT use Bash, Write, or Edit in this phase. Just read and plan.\n\n" +
		"### Phase 2 — EXECUTE (turns 6+): Build, verify, iterate\n" +
		"Per-step loop:\n" +
		"1. **Read** the files you need to modify (re-read if it's been a few turns).\n" +
		"2. **Edit** or **Write** — make ONE logical change at a time.\n" +
		"3. **Evaluate** with a compile/test command to verify correctness.\n" +
		"   - Evaluate spawns a read-only sub-agent that cannot modify files.\n" +
		"   - Use it after every code change: write → evaluate → fix → repeat.\n" +
		"4. If Evaluate returns FAIL, read the error, fix, and re-evaluate.\n" +
		"5. If a command fails twice, change your approach rather than retrying.\n" +
		"6. Mark each TodoWrite item complete before moving to the next.\n" +
		"7. If stuck after 3 attempts, re-read the plan — you may have misunderstood.\n\n" +
		"### Tool Guidelines\n" +
		"- Agent and TaskCreate spawn background workers — use for independent parallel subtasks, not to avoid work.\n" +
		"- WebSearch/WebFetch are SECONDARY — exhaust local tools first.\n" +
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
