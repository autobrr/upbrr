// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	cliPanelArguments = iota
	cliPanelRelease
	cliPanelLogs
	cliPanelFlow
	cliPanelTrackers
	cliPanelCount
)

var cliPanelNames = [cliPanelCount]string{"CLI arguments", "Release details", "Logging output", "Application flow", "Trackers"}

type cliChoiceItem struct{ label, value string }

func (i cliChoiceItem) Title() string       { return i.label }
func (i cliChoiceItem) Description() string { return "" }
func (i cliChoiceItem) FilterValue() string { return i.label }

// cliTUIModel owns terminal controls and private buffers on the Tea update loop.
// Producers communicate through the bridge rather than mutating model state.
type cliTUIModel struct {
	ctx                                           context.Context
	bridge                                        *cliTUIBridge
	cancel                                        func()
	width, height, focus                          int
	view                                          cliView
	telemetry                                     []cliTelemetry
	panels                                        [cliPanelCount]viewport.Model
	evidence                                      viewport.Model
	logs                                          []cliLogLine
	dropped, evicted, unseen                      uint64
	follow                                        bool
	question                                      *cliPromptRequest
	field                                         textinput.Model
	choices                                       list.Model
	selected                                      map[string]bool
	selectedValues                                []string
	confirmed                                     bool
	spinner                                       spinner.Model
	progress                                      progress.Model
	help                                          help.Model
	noColor                                       bool
	canceling                                     bool
	dashboard, keepOpen, completed                bool
	completionText                                string
	launch, launchPending                         bool
	draftFocus                                    int
	sources                                       []textinput.Model
	sourceIndexes                                 []int
	arguments                                     textarea.Model
	initialArguments, protectedNotice, validation string
}

// Formatting never includes the private field or draft buffers.
func (*cliTUIModel) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "cliTUIModel{private terminal state}")
}

func newCLITextInput() textinput.Model {
	field := textinput.New()
	field.CharLimit = 16384
	field.KeyMap.Paste.SetEnabled(false)
	field.SetStyles(textinput.Styles{})
	return field
}

func newCLITUIModel(ctx context.Context, bridge *cliTUIBridge, cancel func(), args, paths []string, debug, launch, noColor bool) *cliTUIModel {
	m := &cliTUIModel{
		ctx:      ctx,
		bridge:   bridge,
		cancel:   cancel,
		follow:   true,
		noColor:  noColor,
		launch:   launch,
		focus:    cliPanelFlow,
		field:    newCLITextInput(),
		spinner:  spinner.New(),
		progress: progress.New(progress.WithDefaultBlend()),
		help:     help.New(),
	}
	m.view = safeCLIView(cliView{
		Source:    strings.Join(paths, "\n"),
		Arguments: safeCLIInvocation(args),
		Debug:     debug,
		Stage:     "Starting",
	})
	for i := range m.panels {
		m.panels[i] = viewport.New()
		m.panels[i].SoftWrap = true
		m.panels[i].MouseWheelEnabled = false
	}
	m.panels[cliPanelTrackers].SoftWrap = false
	m.evidence = viewport.New()
	m.evidence.SoftWrap = true
	m.evidence.MouseWheelEnabled = false
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.Styles.NormalTitle = lipgloss.NewStyle()
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().Bold(true)
	m.choices = list.New(nil, delegate, 30, 4)
	m.choices.SetShowTitle(false)
	m.choices.SetShowStatusBar(false)
	m.choices.SetShowHelp(false)
	m.choices.SetFilteringEnabled(false)
	m.choices.DisableQuitKeybindings()
	m.choices.KeyMap.ShowFullHelp.SetEnabled(false)
	m.choices.KeyMap.CloseFullHelp.SetEnabled(false)
	m.arguments = textarea.New()
	m.arguments.CharLimit = 65536
	m.arguments.ShowLineNumbers = false
	m.arguments.KeyMap.Paste.SetEnabled(false)
	m.arguments.KeyMap.CopySelection.SetEnabled(false)
	initialArguments, opaque, _ := cliDraftArguments(args)
	m.initialArguments = initialArguments
	m.protectedNotice = cliProtectedArgumentNotice(opaque)
	m.arguments.SetValue(m.initialArguments)
	if len(paths) == 0 {
		paths = []string{""}
	}
	for index, path := range paths {
		field := newCLITextInput()
		field.SetValue(safeTerminalText(path))
		m.sources = append(m.sources, field)
		m.sourceIndexes = append(m.sourceIndexes, index)
	}
	if launch {
		m.focus = cliPanelArguments
		m.sources[0].Focus()
	}
	return m
}

func (m *cliTUIModel) Init() tea.Cmd {
	close(m.bridge.ready)
	return tea.Batch(m.bridge.receive(m.ctx), m.spinner.Tick)
}

func (m *cliTUIModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.question != nil {
		select {
		case <-m.question.done:
			m.clearQuestion()
		default:
		}
	}
	switch msg := message.(type) {
	case cliCompletion:
		m.clearQuestion()
		_, receive := m.Update(m.bridge.drain())
		m.completed = true
		m.completionText, m.view.Stage = "Workflow completed.", "Completed"
		if msg.err != nil {
			m.completionText = "Workflow failed.\n" + safeDiagnosticText(msg.err.Error())
			m.view.Stage = "Failed"
		}
		m.refreshPanels()
		if m.keepOpen && !m.canceling && !msg.canceled && m.ctx.Err() == nil && !errors.Is(msg.err, context.Canceled) &&
			!errors.Is(msg.err, errCLIUserInterrupt) && !errors.Is(msg.err, errCLIShutdownUnconfirmed) {
			return m, receive
		}
		return m, tea.Quit
	case cliBridgeUpdate:
		if msg.view != nil && msg.view.Epoch >= m.view.Epoch && msg.view.Item >= m.view.Item &&
			(msg.view.WorkflowID != m.view.WorkflowID || msg.view.Revision >= m.view.Revision) {
			if m.question != nil && m.question.prompt.WorkflowID != "" &&
				(m.question.prompt.WorkflowID != msg.view.WorkflowID || m.question.prompt.Revision != msg.view.Revision) {
				answer := cliPromptAnswer(m.question.prompt, "", false)
				answer.Stale = true
				select {
				case m.question.reply <- answer:
				default:
				}
				m.clearQuestion()
			}
			m.view = *msg.view
		}
		if msg.progress != nil && msg.progress.Epoch >= m.view.Epoch && msg.progress.Item >= m.view.Item &&
			(msg.progress.WorkflowID == "" || msg.progress.WorkflowID == string(m.view.WorkflowID)) &&
			(msg.progress.OperationID == "" || msg.progress.OperationID == string(m.view.OperationID)) {
			m.view.Stage = msg.progress.Phase
			m.view.Completed, m.view.Total = msg.progress.Completed, msg.progress.Total
		}
		m.logs = append(m.logs, msg.logs...)
		m.telemetry = slices.DeleteFunc(msg.telemetry, func(progress cliTelemetry) bool {
			return progress.Epoch < m.view.Epoch || progress.Item < m.view.Item ||
				(progress.WorkflowID != "" && progress.WorkflowID != string(m.view.WorkflowID)) ||
				(progress.OperationID != "" && progress.OperationID != string(m.view.OperationID))
		})
		if len(m.logs) > cliRetainedLogLimit {
			removed := len(m.logs) - cliRetainedLogLimit
			if !m.follow {
				prefix := m.panels[cliPanelLogs]
				prefix.SetContent(cliLogContent(m.logs[:removed], m.noColor))
				// The formatter ends with a newline; its final empty row remains
				// in the retained content and is not part of the evicted prefix.
				rows := prefix.TotalLineCount() - 1
				m.panels[cliPanelLogs].SetYOffset(max(0, m.panels[cliPanelLogs].YOffset()-rows))
			}
			m.logs = slices.Clone(m.logs[removed:])
			m.evicted += uint64(removed) //nolint:gosec // removed is the positive excess of a slice length, so it cannot overflow uint64.
		}
		m.dropped = msg.dropped
		if !m.follow {
			m.unseen += uint64(len(msg.logs))
		}
		m.refreshPanels()
		return m, m.bridge.receive(m.ctx)
	case cliPromptRequest:
		select {
		case <-msg.done:
			return m, m.bridge.receive(m.ctx)
		default:
		}
		m.clearQuestion()
		m.question = &msg
		m.confirmed = msg.prompt.DefaultYes
		m.selected = make(map[string]bool)
		m.validation = ""
		m.field = newCLITextInput()
		if msg.prompt.Kind == cliPromptSecret {
			m.field.EchoMode = textinput.EchoPassword
		}
		m.field.Focus()
		items := make([]list.Item, len(msg.prompt.Options))
		for i, option := range msg.prompt.Options {
			items[i] = cliChoiceItem{label: option.Label, value: option.Value}
		}
		m.choices.SetItems(items)
		m.choices.ResetSelected()
		m.evidence.SetContent(msg.prompt.Question + "\n" + msg.prompt.Evidence + "\n" + msg.prompt.Help)
		m.evidence.GotoTop()
		if !m.launch {
			m.focus = cliPanelFlow
		}
		m.refreshPanels()
		return m, m.bridge.receive(m.ctx)
	case cliLaunchResult:
		m.launchPending = false
		if msg.err != nil {
			m.validation = safeDiagnosticText(msg.err.Error())
			if strings.Contains(m.validation, "sensitive") || strings.Contains(m.validation, "protected") {
				m.arguments.SetValue(m.initialArguments)
			}
			return m, nil
		}
		m.launch = false
		m.arguments.SetValue("")
		for i := range m.sources {
			m.sources[i].Reset()
		}
		m.focus = cliPanelFlow
		m.validation = ""
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case tea.KeyPressMsg:
		if m.dashboard {
			return m, nil
		}
		if m.completed && (msg.String() == "q" || msg.String() == "esc" || msg.String() == "ctrl+c") {
			return m, tea.Quit
		}
		if msg.String() == "ctrl+c" {
			if !m.canceling {
				m.canceling = true
				m.clearQuestion()
			}
			m.cancel()
			return m, nil
		}
		if m.width < 40 || m.height < 12 || m.canceling {
			return m, nil
		}
		if m.launch {
			return m, m.updateDraft(msg)
		}
		if msg.String() == "f6" {
			m.focus = cliPanelFlow
			return m, nil
		}
		if msg.String() == "f7" {
			m.focus = cliPanelLogs
			return m, nil
		}
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			delta := 1
			if msg.String() == "shift+tab" {
				delta = -1
			}
			m.focus = (m.focus + delta + cliPanelCount) % cliPanelCount
			return m, nil
		}
		if m.focus == cliPanelFlow && m.question != nil {
			return m, m.updateQuestion(msg)
		}
		if m.focus == cliPanelLogs {
			if msg.String() == "end" {
				m.follow = true
				m.unseen = 0
				m.panels[cliPanelLogs].GotoBottom()
				return m, nil
			}
			if msg.String() == "pgup" || msg.String() == "up" || msg.String() == "home" {
				m.follow = false
			}
		}
		var command tea.Cmd
		m.panels[m.focus], command = m.panels[m.focus].Update(msg)
		return m, command
	case spinner.TickMsg:
		if m.completed {
			return m, nil
		}
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(msg)
		if m.canceling {
			return m, nil
		}
		return m, command
	}
	var command tea.Cmd
	if m.launch {
		if m.draftFocus < len(m.sources) {
			m.sources[m.draftFocus], command = m.sources[m.draftFocus].Update(message)
		} else if m.draftFocus == len(m.sources) {
			m.arguments, command = m.arguments.Update(message)
		}
	} else if m.question != nil && m.focus == cliPanelFlow {
		m.field, command = m.field.Update(message)
	}
	return m, command
}

// clearQuestion clears private answer state and the displayed evidence on
// reply, cancellation, or replacement by a newer workflow revision.
func (m *cliTUIModel) clearQuestion() {
	m.field.Reset()
	m.field.Blur()
	m.question = nil
	m.selected = nil
	m.selectedValues = nil
	m.evidence.SetContent("")
}

func (m *cliTUIModel) updateQuestion(msg tea.KeyPressMsg) tea.Cmd {
	prompt := m.question.prompt
	stroke := msg.String()
	if stroke == "esc" {
		return nil
	}
	if stroke == "pgup" || stroke == "pgdown" {
		var cmd tea.Cmd
		m.evidence, cmd = m.evidence.Update(msg)
		return cmd
	}
	if prompt.Kind == cliPromptConfirm {
		switch stroke {
		case "left", "n":
			m.confirmed = false
		case "right", "y":
			m.confirmed = true
		case "enter":
			m.reply("", m.confirmed)
		}
		return nil
	}
	if prompt.Kind == cliPromptSelect || prompt.Kind == cliPromptMultiSelect {
		if stroke == "space" && prompt.Kind == cliPromptMultiSelect {
			if item, ok := m.choices.SelectedItem().(cliChoiceItem); ok {
				m.selected[item.value] = !m.selected[item.value]
				if m.selected[item.value] {
					m.selectedValues = append(m.selectedValues, item.value)
				} else {
					m.selectedValues = slices.DeleteFunc(m.selectedValues, func(value string) bool { return value == item.value })
				}
			}
			m.refreshChoices()
			return nil
		}
		if stroke == "enter" {
			if prompt.Kind == cliPromptSelect {
				if item, ok := m.choices.SelectedItem().(cliChoiceItem); ok {
					m.reply(item.value, false)
				}
			} else {
				values := m.selectedValues
				if len(values) == 0 {
					m.validation = "Select at least one option using Space."
				} else {
					m.reply(strings.Join(values, ","), false)
				}
			}
			return nil
		}
		var command tea.Cmd
		m.choices, command = m.choices.Update(msg)
		return command
	}
	if stroke == "enter" {
		value := strings.TrimSpace(m.field.Value())
		if prompt.Required && value == "" {
			m.validation = "This field is required."
			return nil
		}
		m.reply(value, false)
		return nil
	}
	var command tea.Cmd
	m.field, command = m.field.Update(msg)
	return command
}

func (m *cliTUIModel) refreshChoices() {
	items := make([]list.Item, len(m.question.prompt.Options))
	for i, option := range m.question.prompt.Options {
		mark := "[ ] "
		if m.selected[option.Value] {
			mark = "[x] "
		}
		items[i] = cliChoiceItem{label: mark + option.Label, value: option.Value}
	}
	m.choices.SetItems(items)
}

func (m *cliTUIModel) reply(text string, confirmed bool) {
	if m.question == nil {
		return
	}
	answer := cliPromptAnswer(m.question.prompt, text, confirmed)
	select {
	case <-m.question.done:
	case m.question.reply <- answer:
	default:
	}
	m.clearQuestion()
	m.validation = ""
}

func (m *cliTUIModel) updateDraft(msg tea.KeyPressMsg) tea.Cmd {
	if m.launchPending {
		return nil
	}
	stroke := msg.String()
	if stroke == "ctrl+n" && m.draftFocus < len(m.sources) {
		m.sources[m.draftFocus].Blur()
		m.sources = append(m.sources, newCLITextInput())
		m.sourceIndexes = append(m.sourceIndexes, -1)
		m.draftFocus = len(m.sources) - 1
		return m.sources[m.draftFocus].Focus()
	}
	if stroke == "ctrl+d" && m.draftFocus < len(m.sources) && len(m.sources) > 1 {
		m.sources = slices.Delete(m.sources, m.draftFocus, m.draftFocus+1)
		m.sourceIndexes = slices.Delete(m.sourceIndexes, m.draftFocus, m.draftFocus+1)
		m.draftFocus = min(m.draftFocus, len(m.sources)-1)
		return m.sources[m.draftFocus].Focus()
	}
	if stroke == "tab" || stroke == "shift+tab" {
		for i := range m.sources {
			m.sources[i].Blur()
		}
		m.arguments.Blur()
		delta := 1
		if stroke == "shift+tab" {
			delta = -1
		}
		m.draftFocus = (m.draftFocus + delta + len(m.sources) + 2) % (len(m.sources) + 2)
		if m.draftFocus < len(m.sources) {
			m.focus = cliPanelArguments
			return m.sources[m.draftFocus].Focus()
		}
		if m.draftFocus == len(m.sources) {
			m.focus = cliPanelArguments
			return m.arguments.Focus()
		}
		m.focus = cliPanelFlow
		return nil
	}
	if stroke == "enter" && m.draftFocus == len(m.sources)+1 {
		sources := make([]string, len(m.sources))
		for i := range sources {
			sources[i] = m.sources[i].Value()
		}
		reply := make(chan cliLaunchResult, 1)
		request := cliLaunchRequest{
			Sources:       sources,
			SourceIndexes: slices.Clone(m.sourceIndexes),
			Arguments:     m.arguments.Value(),
			Changed:       m.arguments.Value() != m.initialArguments,
			Reply:         reply,
		}
		select {
		case m.bridge.launch <- request:
			m.launchPending = true
		case <-m.ctx.Done():
			return nil
		default:
			return nil
		}
		return func() tea.Msg {
			select {
			case result := <-reply:
				return result
			case <-m.ctx.Done():
				return cliCompletion{err: m.ctx.Err()}
			}
		}
	}
	var command tea.Cmd
	if m.draftFocus < len(m.sources) {
		if stroke == "enter" {
			return nil
		}
		m.sources[m.draftFocus], command = m.sources[m.draftFocus].Update(msg)
	} else if m.draftFocus == len(m.sources) {
		m.arguments, command = m.arguments.Update(msg)
	}
	return command
}

func (m *cliTUIModel) refreshPanels() {
	input := m.view.Source
	if m.view.ItemsTotal > 0 {
		input = fmt.Sprintf("Item %d/%d: %s", m.view.Item, m.view.ItemsTotal, m.view.Source)
	}
	m.panels[cliPanelArguments].SetContent(input + "\n" + m.view.Arguments)
	var trackers strings.Builder
	for _, lane := range m.view.Lanes {
		name := lane.ID
		if !m.noColor {
			color := ""
			switch lane.Status {
			case cliLanePending:
				color = "3"
			case cliLaneReady:
				color = "2"
			case cliLaneBlocked:
				color = "1"
			}
			name = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(name)
		}
		fmt.Fprintf(&trackers, "%s:", name)
		if lane.URL != "" {
			fmt.Fprintf(&trackers, " %s •", lane.URL)
		}
		fmt.Fprintf(&trackers, " %s", lane.State)
		if lane.Reason != "" {
			fmt.Fprintf(&trackers, " (%s)", strings.Join(strings.Fields(lane.Reason), " "))
		}
		trackers.WriteByte('\n')
	}
	m.panels[cliPanelTrackers].SetContent(trackers.String())
	m.panels[cliPanelRelease].SetContent(strings.TrimSpace(m.view.Summary))
	m.panels[cliPanelLogs].SetContent(cliLogContent(m.logs, m.noColor))
	if m.follow {
		m.panels[cliPanelLogs].GotoBottom()
	}
}

func cliLogContent(lines []cliLogLine, noColor bool) string {
	var logs strings.Builder
	for _, line := range lines {
		logs.WriteString(line.Time)
		logs.WriteByte(' ')
		if line.Level != "" {
			logs.WriteString(cliLogLevelStyle(line.Level, noColor))
			logs.WriteString(": ")
		}
		logs.WriteString(line.Text)
		logs.WriteByte('\n')
	}
	return logs.String()
}

type cliHelpKeys struct{}

func (cliHelpKeys) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("f6"), key.WithHelp("F6", "answer")),
		key.NewBinding(key.WithKeys("f7"), key.WithHelp("F7", "logs")),
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("Tab/Shift+Tab", "focus")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("Ctrl+C", "cancel")),
		key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp/PgDn", "scroll")),
	}
}
func (k cliHelpKeys) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }
