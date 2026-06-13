package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/raydraw/ergate/internal/llm"
)

// Registry manages all available tools.
// Tool configs and names are cached after first computation and invalidated
// when tools are registered. This avoids per-turn allocation + sort overhead
// when the tool set is stable (the common case).
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	dirty   bool // cache stale after Register/RegisterRaw
	cachedC []llm.ToolConfig
	cachedN []string
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = t
	r.dirty = true
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// RegisterRaw adds an already-constructed tool without duplicate checking.
func (r *Registry) RegisterRaw(t Tool) {
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.dirty = true
	r.mu.Unlock()
}

// ToolNames returns the names of all registered tools.
func (r *Registry) ToolNames() []string {
	r.mu.RLock()
	if !r.dirty && r.cachedN != nil {
		defer r.mu.RUnlock()
		return r.cachedN
	}
	r.mu.RUnlock()
	r.rebuildCaches()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cachedN
}

// ToolConfigs returns the tool configurations for the LLM API.
// Results are cached and rebuilt only when tools are registered/unregistered.
// Sorted alphabetically for deterministic serialization (prefix-cache stability).
func (r *Registry) ToolConfigs() []llm.ToolConfig {
	r.mu.RLock()
	if !r.dirty && r.cachedC != nil {
		defer r.mu.RUnlock()
		return r.cachedC
	}
	r.mu.RUnlock()
	r.rebuildCaches()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cachedC
}

// rebuildCaches regenerates both cached names and configs under write lock.
func (r *Registry) rebuildCaches() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty {
		return // another goroutine already rebuilt
	}

	// Rebuild names.
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	r.cachedN = names

	// Rebuild configs.
	configs := make([]llm.ToolConfig, 0, len(r.tools))
	for _, name := range names {
		t := r.tools[name]
		if !t.IsEnabled() {
			continue
		}
		configs = append(configs, llm.ToolConfig{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	r.cachedC = configs
	r.dirty = false
}

// Searchable is an optional interface for tools with search hints.
type Searchable interface {
	SearchHint() string
}

// Search returns tools matching a keyword query.
func (r *Registry) Search(query string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(query)
	var results []Tool
	for _, t := range r.tools {
		if !t.IsEnabled() {
			continue
		}
		if strings.Contains(strings.ToLower(t.Name()), lower) ||
			strings.Contains(strings.ToLower(t.Description()), lower) {
			results = append(results, t)
			continue
		}
		if s, ok := t.(Searchable); ok {
			if strings.Contains(strings.ToLower(s.SearchHint()), lower) {
				results = append(results, t)
			}
		}
	}
	return results
}

// Execute runs a tool by name with the given input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage, exec *ExecContext) (*ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %q", name)
	}
	if !t.IsEnabled() {
		return nil, fmt.Errorf("tool %q is disabled", name)
	}

	// Check permissions
	if exec.PermissionMgr != nil {
		if err := exec.PermissionMgr.Check(ctx, name, input); err != nil {
			return &ToolResult{
				Success: false,
				Content: fmt.Sprintf("Permission denied for %s: %v", name, err),
			}, nil
		}
	}

	return t.Execute(ctx, input, exec)
}
