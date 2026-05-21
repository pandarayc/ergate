package tool

// RegisterBuiltins registers all built-in tools with the registry.
func RegisterBuiltins(reg *Registry, todo *TodoManager) {
	reg.Register(NewBashTool())
	reg.Register(NewReadTool())
	reg.Register(NewWriteTool())
	reg.Register(NewEditTool())
	reg.Register(NewGrepTool())
	reg.Register(NewGlobTool())
	reg.Register(NewWebFetchTool())
	reg.Register(NewWebSearchTool())
	reg.Register(NewToolSearchTool(reg))
	reg.Register(NewTodoWriteTool(todo))
}
