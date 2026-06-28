package tool

// RegisterBuiltins registers all built-in tools with the registry.
// All tools are available in every mode (TUI, headless, bench) —
// Ergate does not maintain separate tool sets for different environments.
func RegisterBuiltins(reg *Registry, todo *TodoManager) {
	reg.Register(NewBashTool())
	reg.Register(NewReadTool())
	reg.Register(NewWriteTool())
	reg.Register(NewEditTool())
	reg.Register(NewGrepTool())
	reg.Register(NewGlobTool())
	reg.Register(NewToolSearchTool(reg))
	reg.Register(NewTodoWriteTool(todo))
	reg.Register(NewEvaluateTool())
	reg.Register(NewWebFetchTool())
	reg.Register(NewWebSearchTool())
}
