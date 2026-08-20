package main

import (
	"strings"

	"github.com/lxn/walk"
)

type row struct {
	Name string
	// Status is the pre-computed text for the second column.
	Status string
	// Killable marks a running process that matches neither whitelist.txt nor
	// the agent's built-in protected list -- i.e. one the agent would kill in
	// whitelist mode.
	Killable bool
}

// rowModel is a filterable TableView model shared by both lists.
type rowModel struct {
	walk.TableModelBase

	all          []row
	rows         []row
	filter       string
	onlyKillable bool
}

func (m *rowModel) RowCount() int { return len(m.rows) }

func (m *rowModel) Value(r, col int) interface{} {
	if r < 0 || r >= len(m.rows) {
		return ""
	}
	switch col {
	case 0:
		return m.rows[r].Name
	case 1:
		return m.rows[r].Status
	}
	return ""
}

func (m *rowModel) SetRows(rows []row) {
	m.all = rows
	m.apply()
}

func (m *rowModel) SetFilter(f string) {
	m.filter = strings.ToLower(strings.TrimSpace(f))
	m.apply()
}

func (m *rowModel) SetOnlyKillable(v bool) {
	m.onlyKillable = v
	m.apply()
}

func (m *rowModel) apply() {
	rows := make([]row, 0, len(m.all))
	for _, r := range m.all {
		if m.onlyKillable && !r.Killable {
			continue
		}
		if m.filter != "" && !strings.Contains(r.Name, m.filter) {
			continue
		}
		rows = append(rows, r)
	}
	m.rows = rows
	m.PublishRowsReset()
}

// namesAt maps view indexes (which shift as the filter changes) back to names.
func (m *rowModel) namesAt(indexes []int) []string {
	var names []string
	for _, i := range indexes {
		if i >= 0 && i < len(m.rows) {
			names = append(names, m.rows[i].Name)
		}
	}
	return names
}
