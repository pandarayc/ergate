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
	return "## Execution Strategy\n\n" +

		"### Error Recovery\n" +
		"When a tool fails, analyze the error BEFORE retrying:\n" +
		"1. TLS / certificate errors: install ca-certificates via apt-get, do not search the web\n" +
		"2. Missing dependencies: run apt-get update && apt-get install, do not search the web\n" +
		"3. Build / compile errors: read the specific error output, fix the SOURCE code, not the build flags\n" +
		"4. Command not found: install the missing tool via apt-get\n" +
		"5. Timeout: the command is too slow — try a smaller scope or compile a single file first\n" +
		"6. NEVER call the same failing command more than twice — CHANGE your approach on the third attempt\n\n" +

		"### Tool Selection\n" +
		"- Prefer local operations (Read, Bash, Glob, Grep, Edit, Write) over network tools\n" +
		"- WebSearch / WebFetch: ONLY use when the task explicitly requires external information or downloads\n" +
		"- Bash: always include a description of what the command does and why\n" +
		"- After editing files, VERIFY with Bash (compile, run tests)\n" +
		"- If you need to download archives or source code, use Bash with curl/wget, not WebFetch\n\n" +

		"### Task Execution\n" +
		"- Break complex tasks into small, verifiable steps\n" +
		"- State what you are doing BEFORE calling tools, not after\n" +
		"- If stuck after 3 attempts, re-read the task instruction — you may have misunderstood\n" +
		"- Prefer concrete action over exploration — open the file, run the command, see what happens"
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
