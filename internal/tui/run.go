package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
)

// Run starts the bubbletea TUI.
func Run(cfg *config.Config, eng *engine.Engine, resume bool) error {
	store, _ := session.NewStore(cfg.SessionDir)
	app := NewAppModel(cfg, eng, store, resume)
	p := tea.NewProgram(&app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
