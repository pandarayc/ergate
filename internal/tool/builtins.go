package tool

// RegisterBuiltins registers all built-in tools with the registry.
func RegisterBuiltins(reg *Registry, todo *TodoManager) {
	RegisterLocalTools(reg, todo)
	reg.Register(NewWebFetchTool())
	reg.Register(NewWebSearchTool())
}

// RegisterLocalTools registers only local filesystem tools (no WebFetch/WebSearch).
// Use this for offline/benchmark environments where network tools waste turns.
func RegisterLocalTools(reg *Registry, todo *TodoManager) {
	reg.Register(NewBashTool())
	reg.Register(NewReadTool())
	reg.Register(NewWriteTool())
	reg.Register(NewEditTool())
	reg.Register(NewGrepTool())
	reg.Register(NewGlobTool())
	reg.Register(NewToolSearchTool(reg))
	reg.Register(NewTodoWriteTool(todo))
}
