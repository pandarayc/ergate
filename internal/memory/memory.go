package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MemoryType classifies a memory entry.
type MemoryType string

const (
	TypeUser      MemoryType = "user"
	TypeFeedback  MemoryType = "feedback"
	TypeProject   MemoryType = "project"
	TypeReference MemoryType = "reference"
)

// Entry is a single memory item with parsed frontmatter.
type Entry struct {
	Name        string
	Description string
	Type        MemoryType
	Content     string // raw markdown body (without frontmatter)
}

// Dir returns the memory directory for a project.
func Dir(projectRoot string) string {
	return filepath.Join(projectRoot, ".ergate", "memory")
}

// LoadAll reads all .md files from the memory directory.
func LoadAll(dir string) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read memory dir: %w", err)
	}

	var result []Entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "MEMORY.md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		entry, err := ParseFile(path)
		if err != nil {
			continue // skip unparseable files
		}
		result = append(result, *entry)
	}
	return result, nil
}

// ParseFile parses a memory .md file with YAML frontmatter.
func ParseFile(path string) (*Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	frontmatter, body := extractFrontmatter(content)

	name := strings.TrimSuffix(filepath.Base(path), ".md")

	entry := &Entry{
		Name:    name,
		Content: strings.TrimSpace(body),
	}

	if frontmatter != "" {
		var fm struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
			Type        string `yaml:"type"`
		}
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err == nil {
			if fm.Name != "" {
				entry.Name = fm.Name
			}
			entry.Description = fm.Description
			entry.Type = MemoryType(fm.Type)
		}
	}

	return entry, nil
}

// extractFrontmatter splits YAML frontmatter (between --- markers) from body.
func extractFrontmatter(content string) (frontmatter, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}

	var fmLines []string
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(fmLines, "\n"), strings.Join(lines[i+1:], "\n")
		}
		fmLines = append(fmLines, lines[i])
	}
	return "", content // no closing ---
}

// LoadAgentFile reads AGENTS.md or CLAUDE.md from the project root.
// AGENTS.md takes priority if both exist.
func LoadAgentFile(root string) *Entry {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			return &Entry{Name: name, Content: string(data)}
		}
	}
	return nil
}

// BuildPrompt appends memory entries to a system prompt.
