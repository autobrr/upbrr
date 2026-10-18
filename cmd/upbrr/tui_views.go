// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func cliLogLevelStyle(level string, noColor bool) string {
	if noColor {
		return level
	}
	color := ""
	switch level {
	case "TRACE":
		color = "245"
	case "DEBUG":
		color = "6"
	case "INFO":
		color = "2"
	case "WARN":
		color = "3"
	case "ERROR":
		color = "1"
	}
	if color == "" {
		return level
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(level)
}

func (m *cliTUIModel) resize() {
	width := max(1, m.width-2)
	for i := range m.panels {
		m.panels[i].SetWidth(width)
		m.panels[i].SetHeight(max(1, m.height-10))
	}
	m.evidence.SetWidth(max(1, width-2))
	m.field.SetWidth(max(1, width-4))
	for i := range m.sources {
		m.sources[i].SetWidth(max(1, width-4))
	}
	m.arguments.SetWidth(max(1, width-4))
	m.arguments.SetHeight(2)
	m.help.SetWidth(max(1, m.width))
	m.refreshPanels()
}

func (m *cliTUIModel) panel(index, width, height int, content string) string {
	innerWidth, innerHeight := max(1, width-2), max(1, height-3)
	heading := cliPanelNames[index]
	if m.focus == index && index != cliPanelRelease {
		heading = "> " + heading
	}
	if index == cliPanelRelease {
		heading = ""
		innerHeight = max(1, height-2)
	}
	if index == cliPanelArguments {
		if m.launch {
			heading += " [draft]"
		} else {
			heading += " [read-only]"
		}
	}
	if index == cliPanelTrackers {
		heading += fmt.Sprintf(" (%d)", len(m.view.Lanes))
		if !m.dashboard {
			heading += " • ↑/↓ PgUp/PgDn scroll"
		}
	}
	if index == cliPanelLogs {
		state := "live"
		if !m.follow {
			state = fmt.Sprintf("paused +%d", m.unseen)
		}
		heading += fmt.Sprintf(" [%s omitted:%d earlier:%d]", state, m.dropped, m.evicted)
		if m.focus == index {
			heading += " • ↑/↓ PgUp/PgDn scroll • End follows"
		}
	}
	heading = ansi.Truncate(heading, innerWidth, "…")
	style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Width(width).Height(height).MaxWidth(width).MaxHeight(height)
	if m.focus == index && !m.noColor {
		style = style.BorderForeground(lipgloss.Color("6"))
	}
	if index == cliPanelRelease && m.focus == index && m.noColor {
		style = style.Border(lipgloss.DoubleBorder())
	}
	if content == "" {
		m.panels[index].SetWidth(innerWidth)
		m.panels[index].SetHeight(innerHeight)
		if index == cliPanelLogs && m.follow {
			m.panels[index].GotoBottom()
		}
		content = m.panels[index].View()
	}
	if heading == "" {
		return style.Render(content)
	}
	return style.Render(lipgloss.NewStyle().Bold(true).Render(heading) + "\n" + content)
}

func (m *cliTUIModel) flow(width, height int) string {
	if m.completed {
		return m.completionText + "\n" + m.view.Result
	}
	if m.canceling {
		return "Canceling… Waiting for workflow cleanup.\nRemote submission may require reconciliation."
	}
	if m.launch {
		button := "[ Start ]"
		if m.draftFocus == len(m.sources)+1 {
			button = "> [ Start ]"
		}
		if m.launchPending {
			button = "Validating…"
		}
		return "Edit source paths and CLI arguments, then focus Start and press Enter.\n" + button + "\n" + m.validation + m.protectedNotice
	}
	if m.question == nil {
		status := m.spinner.View() + " " + m.view.Stage
		checkingDuplicates := m.view.Stage == "check-duplicates" || strings.HasPrefix(m.view.Stage, "check-duplicates /")
		separateProgress := checkingDuplicates
		m.progress.SetWidth(max(1, width-5))
		if checkingDuplicates {
			index := slices.IndexFunc(m.telemetry, func(item cliTelemetry) bool {
				return item.Lane == "check-duplicates" && item.Attempt == "" && item.ItemOnly && item.Total > 0
			})
			if index >= 0 {
				item := m.telemetry[index]
				status += fmt.Sprintf("\nDuplicate checks: %d/%d trackers\n", item.Completed, item.Total) +
					m.progress.ViewAs(float64(item.Completed)/float64(item.Total))
			}
		}
		index := slices.IndexFunc(m.telemetry, func(item cliTelemetry) bool {
			return item.Phase == "torrent" && item.Attempt == "" && item.ItemOnly && item.Total > 0
		})
		if index >= 0 {
			item := m.telemetry[index]
			status += fmt.Sprintf("\nTorrent hashing: %d/%d pieces\n", item.Completed, item.Total) +
				m.progress.ViewAs(float64(item.Completed)/float64(item.Total))
			separateProgress = true
		}
		if m.view.Total > 0 {
			if separateProgress {
				status += "\nOverall workflow progress"
			}
			status += "\n" + m.progress.ViewAs(float64(m.view.Completed)/float64(m.view.Total))
		}
		if m.view.Debug {
			status += "\nDebug: tracker submission suppressed."
		}
		return status
	}
	width = max(1, width-2)
	height = max(1, height-3)
	controlHeight := 3
	prompt := m.question.prompt
	if prompt.Kind == cliPromptSelect || prompt.Kind == cliPromptMultiSelect {
		controlHeight = min(6, max(3, height/2))
	}
	m.evidence.SetWidth(width)
	m.evidence.SetHeight(max(1, height-controlHeight-2))
	position := fmt.Sprintf("Evidence %.0f%% • PgUp/PgDn scroll • F6 focuses answer", m.evidence.ScrollPercent()*100)
	controls := ""
	switch prompt.Kind {
	case cliPromptConfirm:
		no, yes := "[ No ]", "[ Yes ]"
		if !m.noColor && m.focus == cliPanelFlow {
			if m.confirmed {
				yes = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true).Render(yes)
			} else {
				no = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true).Render(no)
			}
		}
		if m.confirmed {
			controls = no + "  > " + yes
		} else {
			controls = "> " + no + "  " + yes
		}
		controls += "\nLeft/Right choose • Enter answers"
	case cliPromptText, cliPromptSecret:
		m.field.SetWidth(max(1, width-3))
		controls = m.field.View() + "\nEnter submits this field"
	case cliPromptSelect, cliPromptMultiSelect:
		m.choices.SetSize(width, max(1, controlHeight-1))
		controls = m.choices.View()
		if prompt.Kind == cliPromptMultiSelect {
			controls += "\nSpace selects • Enter submits"
		}
	}
	if m.validation != "" {
		position = m.validation
	}
	return m.evidence.View() + "\n" + ansi.Truncate(position, width, "…") + "\n" + controls
}

func (m *cliTUIModel) draftArguments(width int) string {
	if !m.launch {
		return ""
	}
	index := min(m.draftFocus, len(m.sources)-1)
	label := fmt.Sprintf("Source %d/%d: ", index+1, len(m.sources))
	m.sources[index].SetWidth(max(1, width-ansi.StringWidth(label+m.sources[index].Prompt)-1))
	m.arguments.SetWidth(max(1, width))
	return label + m.sources[index].View() + "\n" + m.arguments.View()
}

func (m *cliTUIModel) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if m.width == 0 || m.height == 0 {
		view.SetContent("Initializing terminal… Ctrl+C cancels.")
		return view
	}
	if m.width < 40 || m.height < 12 {
		view.SetContent(ansi.Truncate("Resize to at least 40×12. Ctrl+C cancels.", max(1, m.width), ""))
		return view
	}
	w, h := m.width, m.height
	argumentHeight := 4
	if m.launch {
		argumentHeight = 5
	}
	footer := m.help.View(cliHelpKeys{})
	if m.completed {
		footer = "q/Esc/Ctrl+C close • Tab/Shift+Tab focus • PgUp/PgDn scroll"
	}
	if m.dashboard {
		footer = "Dashboard • Ctrl+C cancels"
		if m.completed {
			footer = "Dashboard complete • Ctrl+C closes"
		}
	}
	if m.launch {
		footer = "Tab fields • Ctrl+N add source • Ctrl+D remove • Enter on Start"
	}
	footer = ansi.Truncate(footer, w, "…")
	var content string
	switch {
	case w >= 120 && h >= 40:
		left, right := w/2, w-w/2
		argumentHeight += 7
		logHeight := max(6, h/5)
		flowHeight := h - argumentHeight - logHeight - 1
		content = lipgloss.JoinVertical(
			lipgloss.Left,
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				m.panel(cliPanelArguments, left, argumentHeight, m.draftArguments(left-2)),
				m.panel(cliPanelRelease, right, argumentHeight, ""),
			),
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				m.panel(cliPanelFlow, left, flowHeight, m.flow(left-2, flowHeight)),
				m.panel(cliPanelTrackers, right, flowHeight, ""),
			),
			m.panel(cliPanelLogs, w, logHeight, ""),
			footer,
		)
	case w >= 80 && h >= 24:
		left, right := w/2, w-w/2
		argumentHeight = min(argumentHeight+7, h/3)
		detailHeight := h - argumentHeight - 2
		tabs := "Flow • Trackers • Logs (Tab to focus)"
		var body string
		if m.focus == cliPanelLogs {
			body = m.panel(cliPanelLogs, w, detailHeight, "")
		} else {
			body = lipgloss.JoinHorizontal(
				lipgloss.Top,
				m.panel(cliPanelFlow, left, detailHeight, m.flow(left-2, detailHeight)),
				m.panel(cliPanelTrackers, right, detailHeight, ""),
			)
		}
		content = lipgloss.JoinVertical(
			lipgloss.Left,
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				m.panel(cliPanelArguments, left, argumentHeight, m.draftArguments(left-2)),
				m.panel(cliPanelRelease, right, argumentHeight, ""),
			),
			ansi.Truncate(tabs, w, "…"),
			body,
			footer,
		)
	default:
		index := m.focus
		body := ""
		if index == cliPanelFlow {
			body = m.flow(w-2, h-1)
		}
		if m.launch && index == cliPanelArguments {
			body = m.draftArguments(w - 2)
		}
		content = lipgloss.JoinVertical(lipgloss.Left, m.panel(index, w, h-1, body), footer)
	}
	view.SetContent(content)
	return view
}
