package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type uiSizeTick struct{}

func uiSizeTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return uiSizeTick{} })
}

// Screen changes resize the child PTY while Bubble Tea is starting its resize
// listener. Recheck the size so a missed signal cannot leave a dialog-sized UI.
type uiSizedModel struct {
	tea.Model
	width, height int
}

func (m uiSizedModel) Init() tea.Cmd {
	return tea.Batch(m.Model.Init(), uiSizeTickCmd(), tea.RequestBackgroundColor)
}

func (m uiSizedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case uiSizeTick:
		return m, tea.Batch(tea.RequestWindowSize, uiSizeTickCmd())
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		setWorkspaceBackground(msg.IsDark())
	}
	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	return m, cmd
}

func uiSizeFilter(model tea.Model, msg tea.Msg) tea.Msg {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m := model.(uiSizedModel)
		// Filter before the renderer too: unchanged sizes must not repaint the
		// screen, clear a text selection, or cancel a divider drag.
		if size.Width <= 0 || size.Height <= 0 || (size.Width == m.width && size.Height == m.height) {
			return nil
		}
	}
	return msg
}

func runUIProgram(model tea.Model, options ...tea.ProgramOption) (tea.Model, error) {
	options = append(options, tea.WithFilter(uiSizeFilter))
	final, err := tea.NewProgram(uiSizedModel{Model: model}, options...).Run()
	if final != nil {
		return final.(uiSizedModel).Model, err
	}
	return model, err
}
