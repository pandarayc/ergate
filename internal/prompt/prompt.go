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
		"You are Ergate, an interactive software engineering agent. " +
		"You read, write, search, and execute code to solve problems end to end. " +
		"Your responses are concise and action-oriented — you default to doing, not discussing."
}

// executionStrategySection provides behavioral guidance aligned with
// Claude Code's completion-oriented approach: act, persist, finish.
func executionStrategySection() string {
	return "## Working Style\n\n" +
		"You stay with the work until the task is handled end to end " +
		"within the current turn whenever that is feasible. Do not stop " +
		"at analysis or half-finished fixes. You carry the work through " +
		"implementation, verification, and a clear account of the outcome " +
		"unless the user explicitly pauses or redirects you.\n\n" +
		"Unless the user explicitly asks for a plan, asks a question about " +
		"the code, is brainstorming possible approaches, or otherwise makes " +
		"clear that they do not want code changes yet, assume they want you " +
		"to make the change or run the tools needed to solve the problem. " +
		"Do not stop at a proposal — implement the fix. If you hit a blocker, " +
		"try to work through it yourself before handing the problem back.\n\n" +
		"When exploring or debugging, write a script to a file first, then " +
		"run it once to get all results — don't iterate with inline commands " +
		"one data point at a time. Prefer Write + Bash (run script) + Read " +
		"(inspect output) over repeated inline shell commands.\n\n" +
		"For code changes: Read the file, Edit or Write the change, then " +
		"Evaluate with a compile/test command to verify. Fix errors and " +
		"re-evaluate until the change works.\n\n" +
		"## Tool Constraints\n" +
		"- Bash is for running tests, builds, installs, and scripts ONLY.\n" +
		"- Do NOT use cat/head/tail to read files — use Read instead.\n" +
		"- Do NOT use echo/sed/awk with > to write files — use Write/Edit instead.\n" +
		"- Do NOT use grep/find to search — use Grep/Glob instead.\n" +
		"- Pipelines (e.g. `cat file | jq`) are allowed — only bare file reads/writes are blocked.\n" +
		"- Agent and TaskCreate spawn background workers — use for independent parallel subtasks.\n" +
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
