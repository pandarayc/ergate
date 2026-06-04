package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/filehistory"
	"github.com/raydraw/ergate/internal/hooks"
	"github.com/raydraw/ergate/internal/llm"
	_ "github.com/raydraw/ergate/internal/llm/provider" // register all providers
	"github.com/raydraw/ergate/internal/mcp"
	"github.com/raydraw/ergate/internal/planmode"
	"github.com/raydraw/ergate/internal/worktree"
	"github.com/raydraw/ergate/internal/memory"
	"github.com/raydraw/ergate/internal/skill"
	"github.com/raydraw/ergate/internal/task"
	"github.com/raydraw/ergate/internal/tool"
	"github.com/raydraw/ergate/internal/tui"
)

// SetupEngine creates the LLM client and tool registry.
func SetupEngine(cfg *config.Config) (llm.LLMClient, *tool.Registry, *skill.Registry, *tool.TodoManager, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, nil, nil, err
	}

	pc := cfg.ActiveProviderConfig()
	client, err := llm.NewLLMClient(cfg.CompatProvider(), pc.APIKey, pc.BaseURL)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	todoMgr := tool.NewTodoManager()
	toolReg := tool.NewRegistry()
	tool.RegisterBuiltins(toolReg, todoMgr)

	// Load skills
	skillReg := skill.NewRegistry()
	cwd, _ := os.Getwd()
	skillReg.LoadDir(filepath.Join(cwd, ".ergate", "skills"))

	// MCP: connect to configured servers and register their tools
	if cfg.EnableMCP {
		connectMCPServers(cwd, toolReg)
	}

	return client, toolReg, skillReg, todoMgr, nil
}

// CreateEngine creates the engine with permissions wired.
func CreateEngine(cfg *config.Config, client llm.LLMClient, registry *tool.Registry, skillReg *skill.Registry, todoMgr *tool.TodoManager) *engine.Engine {
	cwd, _ := os.Getwd()

	// Build engine context
	permMode := tool.PermModeDefault
	switch cfg.PermissionMode {
	case "always":
		permMode = tool.PermModeDontAsk
	case "bypass":
		permMode = tool.PermModeBypassPermissions
	}

	taskReg := task.NewRegistry()
	memDir := memory.Dir(cwd)
	ectx := engine.Context{
		Skills:        skillReg,
		Hooks:         hooks.NewManager(),
		FileTracker:   filehistory.NewTracker(cwd),
		PlanMgr:       planmode.NewManager(),
		TodoMgr:       todoMgr,
		TaskNotify:    taskReg.NotifyChan(),
		TranscriptDir: filepath.Join(cwd, ".ergate", "sessions"),
		PermMgr:       tool.NewPermissionManager(string(cfg.PermissionMode), nil),
		PermCtx: tool.PermissionContext{
			Mode:             permMode,
			AlwaysAllowRules: make(map[string][]tool.PermissionRule),
			AlwaysDenyRules:  make(map[string][]tool.PermissionRule),
			AlwaysAskRules:   make(map[string][]tool.PermissionRule),
		},
	}
	if entries, err := memory.LoadAll(memDir); err == nil {
		ectx.Memory = entries
		ectx.Agent = memory.LoadAgentFile(cwd)
	}

	eng := engine.New(cfg, client, registry, ectx)

	// Register tools
	registry.Register(skill.NewLoadSkillTool(skillReg))
	registry.Register(planmode.NewEnterPlanTool(ectx.PlanMgr))
	registry.Register(planmode.NewExitPlanTool(ectx.PlanMgr))
	worktreeMgr := worktree.NewManager()
	registry.Register(worktree.NewEnterWorktreeTool(worktreeMgr))
	registry.Register(worktree.NewExitWorktreeTool(worktreeMgr))
	registry.Register(task.NewCreateTool(taskReg))
	registry.Register(task.NewOutputTool(taskReg))
	registry.Register(task.NewStopTool(taskReg))
	registry.Register(task.NewListTool(taskReg))
	registry.Register(task.NewAgentTool(taskReg, cfg.SubagentModelName(), func(ctx context.Context, prompt, model string, maxTurns int) string {
		events := make(chan engine.Event, 64)
		go func() {
			_ = eng.RunSubAgent(ctx, prompt, model, maxTurns, events)
		}()
		var result strings.Builder
		for event := range events {
			if event.Type == engine.EventText {
				if text, ok := event.Data.(string); ok {
					result.WriteString(text)
				}
			}
		}
		return result.String()
	}))
	registry.Register(memory.NewWriteTool(memDir, func(path string) {
		memory.UpdateMEMORYMD(memDir, filepath.Base(path))
	}))

	return eng
}

// StartTUI starts the bubbletea TUI.
func StartTUI(cfg *config.Config, eng *engine.Engine, resume bool) error {
	return tui.Run(cfg, eng, resume)
}

// connectMCPServers reads MCP server configs from .ergate/mcp.json and registers their tools.
func connectMCPServers(cwd string, reg *tool.Registry) {
	mcpConfigPath := filepath.Join(cwd, ".ergate", "mcp.json")
	data, err := os.ReadFile(mcpConfigPath)
	if err != nil {
		return // no mcp config
	}

	var servers []struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Args    []string `json:"args"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(data, &servers); err != nil {
		return
	}

	for _, srv := range servers {
		var transport mcp.Transport
		if srv.Command != "" {
			t, err := mcp.NewStdioTransport(srv.Command, srv.Args...)
			if err != nil {
				continue
			}
			transport = t
		} else if srv.URL != "" {
			transport = mcp.NewHTTPTransport(srv.URL)
		} else {
			continue
		}

		client, err := mcp.NewClient(transport)
		if err != nil {
			transport.Close()
			continue
		}

		for _, mcpTool := range client.Tools() {
			reg.Register(NewMCPTool(mcpTool, client))
		}
	}
}

// NewMCPTool wraps an MCP tool as a Tool.
func NewMCPTool(mcpTool mcp.Tool, client *mcp.Client) tool.Tool {
	return tool.BuildToolFrom(tool.ToolDef{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
		InputSchema: mcpTool.InputSchema,
		Execute: func(ctx context.Context, input json.RawMessage, exec *tool.ExecContext) (*tool.ToolResult, error) {
			result, err := client.CallTool(mcpTool.Name, input)
			if err != nil {
				return &tool.ToolResult{Success: false, Content: err.Error()}, nil
			}
			var content string
			for _, item := range result.Content {
				content += item.Text
			}
			return &tool.ToolResult{Success: !result.IsError, Content: content}, nil
		},
	})
}
