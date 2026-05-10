package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"tui-canvas/internal/protocol"
)

// NewTestModel creates a Model suitable for unit tests (no real socket connection).
func NewTestModel() (*Model, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return nil, err
	}
	return &Model{
		ch:              make(chan tea.Msg, 64),
		renderer:        renderer,
		renderedEntries: make(map[string]string),
	}, nil
}

func (m *Model) HandleSocketMsgForTest(raw []byte) {
	m.handleSocketMsg(raw)
}

func (m *Model) Sessions() []protocol.Session {
	return m.sessions
}

func (m *Model) EnterSearchForTest() {
	m.enterSearch()
}

func (m *Model) ClearSearchForTest() {
	m.clearSearch()
}

func (m *Model) SearchingForTest() bool {
	return m.searching
}

func (m *Model) UpdateSearchForTest(query string) {
	m.updateSearch(query)
}

func (m *Model) SearchHitsForTest() []int {
	return m.searchHits
}
