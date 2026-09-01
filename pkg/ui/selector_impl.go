package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// reactionEmojis defines the available emoji reactions for GitHub comments
var reactionEmojis = []struct {
	name    string // GitHub API name
	display string // Actual emoji for display
}{
	{"+1", "👍"},
	{"-1", "👎"},
	{"laugh", "😄"},
	{"confused", "😕"},
	{"heart", "❤️"},
	{"hooray", "🎉"},
	{"rocket", "🚀"},
	{"eyes", "👀"},
}

// SelectFromList creates an interactive selector for a list of items.
// For more options, use Select() with SelectorOptions.
func SelectFromList[T any](items []T, renderer ItemRenderer[T]) (T, error) {
	return Select(SelectorOptions[T]{
		Items:    items,
		Renderer: renderer,
	})
}

// SelectFromListWithAction creates an interactive selector with a custom action.
// Deprecated: Use Select() with SelectorOptions for new code.
func SelectFromListWithAction[T any](items []T, renderer ItemRenderer[T], customAction CustomAction[T], actionKey string, onOpen CustomAction[T], filterFunc func(T, bool) bool, onSelect CustomAction[T], customActionSecond CustomAction[T], actionKeySecond string) (T, error) {
	return Select(SelectorOptions[T]{
		Items:         items,
		Renderer:      renderer,
		OnSelect:      onSelect,
		OnOpen:        onOpen,
		FilterFunc:    filterFunc,
		ResolveAction: customAction,
		ResolveKey:    actionKey,
		// Note: old API used customActionSecond for R key but it was a sync action.
		// The new API uses editor callbacks for R. For backward compat, we don't
		// support the old sync R action through this wrapper.
		ResolveCommentKey: actionKeySecond,
	})
}

// Select creates an interactive selector with the given options.
// This is the primary API for creating selectors.
func Select[T any](opts SelectorOptions[T]) (T, error) {
	l := newSelectorList(opts.Items, opts, 0, 0)

	m := SelectionModel[T]{
		list:         l,
		items:        opts.Items,
		opts:         opts,
		result:       nil,
		filterActive: opts.FilterDefault,
	}

	// Apply initial filter if FilterDefault is true
	if opts.FilterDefault && opts.FilterFunc != nil {
		m.updateVisibleItems()
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		var zero T
		return zero, err
	}

	final := finalModel.(SelectionModel[T])
	if len(final.result) == 0 {
		var zero T
		return zero, ErrNoSelection
	}
	return final.result[0], nil
}

// newSelectorList builds the bubbles list model backing a selector.
func newSelectorList[T any](items []T, opts SelectorOptions[T], width, height int) list.Model {
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = listItem[T]{value: item, item: opts.Renderer}
	}

	l := list.New(listItems, itemDelegate[T]{renderer: opts.Renderer}, width, height)
	// The tally above the list is rendered from countSummary, which counts
	// entries. The built-in status bar counts rendered rows, so a tree that
	// emits group headers and preview rows would overstate the total.
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.Styles.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	l.Styles.StatusBar = lipgloss.NewStyle().Padding(0, 1)
	l.KeyMap.Quit.SetKeys()
	return l
}

// Init initializes the model
func (m SelectionModel[T]) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model
func (m SelectionModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.windowSize = msg
		headerHeight := 2
		footerHeight := 3
		listHeight := msg.Height - headerHeight - footerHeight
		m.list.SetSize(msg.Width, listHeight)
		m.viewport = viewport.New(msg.Width, listHeight)
		m.viewport.SetContent("")
		return m, nil

	case loadDetailMsg:
		m.loadingDetail = false
		selected := m.list.SelectedItem()
		if selected != nil {
			item := selected.(listItem[T])
			highlightIdx := -1
			if m.commentSelectMode {
				highlightIdx = m.commentSelectIdx
			}
			m.viewport.SetContent(m.opts.Renderer.PreviewWithHighlight(item.value, highlightIdx))
			m.viewport.GotoTop()
		}
		return m, nil

	case refreshFinishedMsg:
		m.refreshing = false
		if msg.err != nil {
			return m, m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Refresh failed: %v", msg.err)))
		}
		if items, ok := msg.items.([]T); ok {
			m.items = items
			cmd := m.updateVisibleItems()

			// If in detail view, refresh the viewport content
			if m.showDetail {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					highlightIdx := -1
					if m.commentSelectMode {
						highlightIdx = m.commentSelectIdx
					}
					m.viewport.SetContent(m.opts.Renderer.PreviewWithHighlight(item.value, highlightIdx))
				}
			}

			return m, tea.Batch(cmd, m.list.NewStatusMessage(Colorize(ColorGreen, fmt.Sprintf("Refreshed: %s", m.countSummary()))))
		}
		return m, nil

	case editorFinishedMsg:
		return m.handleEditorFinished(msg)

	case agentFinishedMsg:
		if msg.err != nil {
			return m, m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Agent error: %v", msg.err)))
		}
		return m, m.list.NewStatusMessage(Colorize(ColorGreen, "Agent completed"))

	case tea.KeyMsg:
		// If showing help overlay, any key dismisses it
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}

		// If showing confirmation, any key dismisses it
		if m.confirmationMessage != "" {
			m.confirmationMessage = ""
			return m, nil
		}

		// Handle reaction mode
		if m.reactionMode {
			switch msg.String() {
			case "enter":
				// Add the reaction
				emoji := reactionEmojis[m.reactionIdx]
				m.reactionMode = false
				if m.opts.ReactionComplete != nil {
					msg, err := m.opts.ReactionComplete(m.reactionItem.value, m.reactionCommentID, emoji.name, emoji.display)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					// Refresh the viewport to show updated reactions
					if m.showDetail {
						m.viewport.SetContent(m.opts.Renderer.PreviewWithHighlight(m.reactionItem.value, -1))
					}
					// Show confirmation dialog with the result
					m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", msg)
					return m, nil
				}
				return m, nil
			case "esc":
				m.reactionMode = false
				return m, m.list.NewStatusMessage("Reaction cancelled")
			case "x":
				// Cycle to next emoji
				m.reactionIdx = (m.reactionIdx + 1) % len(reactionEmojis)
				return m, m.showReactionStatus()
			default:
				// Any other key cancels reaction mode
				m.reactionMode = false
				return m, m.list.NewStatusMessage("Reaction cancelled")
			}
		}

		// Handle apply suggestion preview mode
		if m.applyPreviewMode {
			switch msg.String() {
			case "y", "Y":
				// Confirm and apply the suggestion
				m.applyPreviewMode = false
				var action CustomAction[T]
				if m.applyPreviewWithResolve {
					action = m.opts.ApplySuggestionResolveAction
				} else {
					action = m.opts.ApplySuggestionAction
				}
				if action != nil {
					statusMsg, err := action(m.applyPreviewItem.value)
					if err != nil {
						m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", Colorize(ColorRed, err.Error()))
						return m, nil
					}
					if statusMsg != "" {
						m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", statusMsg)
						return m, nil
					}
				}
				return m, nil
			case "esc", "n", "N":
				// Cancel
				m.applyPreviewMode = false
				return m, m.list.NewStatusMessage("Apply cancelled")
			default:
				// Any other key cancels
				m.applyPreviewMode = false
				return m, m.list.NewStatusMessage("Apply cancelled")
			}
		}

		// Handle comment selection mode
		if m.commentSelectMode {
			switch msg.String() {
			case "enter":
				// Confirm selection - proceed with the action
				return m.executeCommentAction()
			case "esc":
				m.exitCommentSelectMode()
				if m.commentSelectInDetail {
					// Restore detail view without highlight
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						m.viewport.SetContent(m.opts.Renderer.PreviewWithHighlight(item.value, -1))
					}
				}
				return m, m.list.NewStatusMessage("Selection cancelled")
			case "Q", "C", "a", "x":
				if msg.String() == m.commentSelectAction {
					m.cycleCommentSelection()
					if m.commentSelectInDetail {
						m.updateDetailViewWithHighlight()
					}
					return m, m.showCommentSelectStatus()
				}
				// Different action - cancel current and start new
				m.exitCommentSelectMode()
				// Fall through to handle new action
			default:
				// Any other key cancels selection mode
				m.exitCommentSelectMode()
				if m.commentSelectInDetail {
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						m.viewport.SetContent(m.opts.Renderer.PreviewWithHighlight(item.value, -1))
					}
				}
				// Fall through to handle the key normally
			}
		}

		// If filtering is active in the list, let it handle all keys
		if m.list.SettingFilter() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

		// If showing detail view, only handle specific keys
		if m.showDetail {
			switch msg.String() {
			case "esc", "backspace", "left", "h", "q":
				m.showDetail = false
				return m, nil
			case "ctrl+f":
				// Page down in detail view
				m.viewport.PageDown()
				return m, nil
			case "ctrl+b":
				// Page up in detail view
				m.viewport.PageUp()
				return m, nil
			case "r", "u":
				// Execute resolve action from detail view (r=resolve, u=unresolve - both toggle)
				if m.opts.ResolveAction != nil {
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						statusMsg, err := m.opts.ResolveAction(item.value)
						m.showDetail = false
						if err != nil {
							return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
						}
						if statusMsg != "" {
							return m, m.list.NewStatusMessage(statusMsg)
						}
					}
				}
				return m, nil
			case "R", "U":
				// Execute resolve+comment from detail view
				if m.opts.ResolveCommentPrepare != nil {
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						m.showDetail = false
						return m, m.startEditorForAction(item.value, 2)
					}
				}
				return m, nil
			case "Q":
				// Quote reply from detail view
				return m.handleQuoteKey(true)
			case "C":
				// Quote with context from detail view
				return m.handleQuoteContextKey(true)
			case "a":
				// Launch agent from detail view
				return m.handleAgentKey(true)
			case "e":
				// Edit file from detail view
				if m.opts.EditAction != nil {
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						result, err := m.opts.EditAction(item.value)
						if err != nil {
							return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
						}
						if strings.HasPrefix(result, "EDIT_FILE:") {
							parts := strings.SplitN(strings.TrimPrefix(result, "EDIT_FILE:"), ":", 2)
							if len(parts) == 2 {
								lineNum := 0
								_, _ = fmt.Sscanf(parts[1], "%d", &lineNum)
								return m, m.editInEditor(parts[0], lineNum)
							}
						}
						if result != "" {
							return m, m.list.NewStatusMessage(result)
						}
					}
				}
				return m, nil
			case "x":
				// Add reaction from detail view
				return m.handleReactionKey(true)
			case "s":
				// Apply suggestion from detail view (with preview)
				return m.startApplyPreview(false)
			case "S":
				// Apply suggestion and resolve from detail view (with preview)
				return m.startApplyPreview(true)
			case "i":
				// Refresh from detail view
				return m.startRefresh()
			case "o":
				// Open in browser from detail view
				if m.opts.OnOpen != nil {
					selected := m.list.SelectedItem()
					if selected != nil {
						item := selected.(listItem[T])
						statusMsg, err := m.opts.OnOpen(item.value)
						if err != nil {
							return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
						}
						if statusMsg != "" {
							return m, m.list.NewStatusMessage(statusMsg)
						}
					}
				}
				return m, nil
			default:
				// Let viewport handle scrolling
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}

		// Main list view key handling
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "?":
			m.showHelp = true
			return m, nil
		case "q":
			m.result = nil
			return m, tea.Quit
		case "enter", "right", "l":
			selected := m.list.SelectedItem()
			if selected != nil {
				item := selected.(listItem[T])
				if m.opts.OnSelect != nil {
					statusMsg, err := m.opts.OnSelect(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					if statusMsg != "" {
						return m, m.list.NewStatusMessage(statusMsg)
					}
				}
				// Show detail view with loading state
				m.showDetail = true
				m.loadingDetail = true
				m.viewport.SetContent("Loading...")
				return m, func() tea.Msg { return loadDetailMsg{} }
			}
		case "o":
			if m.opts.OnOpen != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					statusMsg, err := m.opts.OnOpen(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					if statusMsg != "" {
						return m, m.list.NewStatusMessage(statusMsg)
					}
				}
			}
			return m, nil
		case "h", "tab":
			// Toggle filter (h = hide/show resolved, tab kept for compatibility)
			if m.opts.FilterFunc != nil {
				m.filterActive = !m.filterActive
				cmd := m.updateVisibleItems()
				if m.filterActive {
					return m, tea.Batch(cmd, m.list.NewStatusMessage("Hiding resolved"))
				}
				return m, tea.Batch(cmd, m.list.NewStatusMessage("Showing all"))
			}
			return m, nil
		case "i":
			// Refresh
			return m.startRefresh()
		case "r", "u":
			// Execute first custom action (r=resolve, u=unresolve - both trigger same action)
			if m.opts.ResolveAction != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					statusMsg, err := m.opts.ResolveAction(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					// Update item in list after action
					m.list.SetItem(m.list.Index(), item)
					if statusMsg != "" {
						return m, m.list.NewStatusMessage(statusMsg)
					}
				}
			}
			return m, nil
		case "R", "U":
			// Execute second action with editor (R=resolve+comment, U=unresolve+comment)
			if m.opts.ResolveCommentPrepare != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					return m, m.startEditorForAction(item.value, 2)
				}
			}
			return m, nil
		case "Q":
			// Execute quote action with editor
			return m.handleQuoteKey(false)
		case "C":
			// Execute quote with context action with editor
			return m.handleQuoteContextKey(false)
		case "a":
			// Execute agent action
			return m.handleAgentKey(false)
		case "e":
			// Execute edit action
			if m.opts.EditAction != nil {
				selected := m.list.SelectedItem()
				if selected != nil {
					item := selected.(listItem[T])
					result, err := m.opts.EditAction(item.value)
					if err != nil {
						return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
					}
					if strings.HasPrefix(result, "EDIT_FILE:") {
						parts := strings.SplitN(strings.TrimPrefix(result, "EDIT_FILE:"), ":", 2)
						if len(parts) == 2 {
							lineNum := 0
							_, _ = fmt.Sscanf(parts[1], "%d", &lineNum)
							return m, m.editInEditor(parts[0], lineNum)
						}
					}
					m.list.SetItem(m.list.Index(), item)
					if result != "" {
						return m, m.list.NewStatusMessage(result)
					}
				}
			}
			return m, nil
		case "s":
			// Apply suggestion (with preview)
			return m.startApplyPreview(false)
		case "S":
			// Apply suggestion and resolve (with preview)
			return m.startApplyPreview(true)
		case "x":
			// Add reaction
			return m.handleReactionKey(false)
		}
	}

	// Default: let list handle navigation
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// startApplyPreview initiates the apply suggestion preview mode
func (m *SelectionModel[T]) startApplyPreview(withResolve bool) (tea.Model, tea.Cmd) {
	if m.opts.ApplySuggestionPreview == nil {
		// No preview configured, show error
		m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", Colorize(ColorRed, "Apply preview not configured"))
		return m, nil
	}

	selected := m.list.SelectedItem()
	if selected == nil {
		return m, nil
	}

	item := selected.(listItem[T])

	// Get the preview diff
	diffPreview, err := m.opts.ApplySuggestionPreview(item.value)
	if err != nil {
		m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", Colorize(ColorRed, err.Error()))
		return m, nil
	}

	// Enter preview mode
	m.applyPreviewMode = true
	m.applyPreviewDiff = diffPreview
	m.applyPreviewItem = item
	m.applyPreviewWithResolve = withResolve

	return m, nil
}

// editInEditor opens the given file path in the user's editor at the specified line
func (m *SelectionModel[T]) editInEditor(filePath string, line int) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	// Format: editor +line filepath (most editors support this)
	var c *exec.Cmd
	if line > 0 {
		c = exec.Command(editor, fmt.Sprintf("+%d", line), filePath)
	} else {
		c = exec.Command(editor, filePath)
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

// startEditorForAction prepares and launches the editor for the given action
func (m *SelectionModel[T]) startEditorForAction(item T, action int) tea.Cmd {
	var preparer EditorPreparer[T]
	switch action {
	case 2:
		preparer = m.opts.ResolveCommentPrepare
	case 3:
		preparer = m.opts.QuotePrepare
	case 4:
		preparer = m.opts.QuoteContextPrepare
	}

	if preparer == nil {
		return nil
	}

	content, err := preparer(item)
	if err != nil {
		return m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "gh-review-responder-*.md")
	if err != nil {
		return m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Failed to create temp file: %v", err)))
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		return m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Failed to write temp file: %v", err)))
	}
	_ = tmpFile.Close()

	m.pendingEditorItem = item
	m.pendingEditorTmpFile = tmpFile.Name()
	m.pendingEditorAction = action

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	c := exec.Command(editor, tmpFile.Name())
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

// handleEditorFinished processes the editor result
func (m SelectionModel[T]) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Editor error: %v", msg.err)))
	}

	if m.pendingEditorTmpFile == "" {
		return m, nil
	}

	// Read the file content
	content, err := os.ReadFile(m.pendingEditorTmpFile)
	_ = os.Remove(m.pendingEditorTmpFile)
	m.pendingEditorTmpFile = ""

	if err != nil {
		return m, m.list.NewStatusMessage(Colorize(ColorRed, fmt.Sprintf("Failed to read temp file: %v", err)))
	}

	sanitized := SanitizeEditorContent(string(content))
	if sanitized == "" {
		return m, m.list.NewStatusMessage("Cancelled (empty content)")
	}

	// Call the appropriate completer
	var completer EditorCompleter[T]
	switch m.pendingEditorAction {
	case 2:
		completer = m.opts.ResolveCommentComplete
	case 3:
		completer = m.opts.QuoteComplete
	case 4:
		completer = m.opts.QuoteContextComplete
	}

	if completer == nil {
		return m, nil
	}

	result, err := completer(m.pendingEditorItem, sanitized)
	if err != nil {
		return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
	}

	// Show confirmation dialog if the result contains a URL
	// This handles both simple URL returns (Q/C actions) and
	// combined status+URL returns (R/U resolve+comment actions)
	if strings.Contains(result, "https://") {
		m.confirmationMessage = fmt.Sprintf("%s\n\nPress any key to continue...", result)
		return m, nil
	}

	if result != "" {
		return m, m.list.NewStatusMessage(result)
	}

	return m, nil
}

// agentCommand returns the coding agent to launch. GH_REVIEW_CONDUCTOR_AGENT
// is honored after the current name so that setups predating the rename keep
// working.
func agentCommand() string {
	for _, name := range []string{"GH_REVIEW_RESPONDER_AGENT", "GH_REVIEW_CONDUCTOR_AGENT"} {
		if agent := os.Getenv(name); agent != "" {
			return agent
		}
	}
	return "claude"
}

// launchAgent starts the configured coding agent with the given prompt
func (m *SelectionModel[T]) launchAgent(prompt string) tea.Cmd {
	parts := strings.Fields(agentCommand())
	args := append(parts[1:], prompt)
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return agentFinishedMsg{err: err}
	})
}

// updateVisibleItems applies filter and updates the list. It returns the
// command SetItems produces, which re-runs any active text filter.
func (m *SelectionModel[T]) updateVisibleItems() tea.Cmd {
	listItems := make([]list.Item, 0, len(m.items))
	for _, item := range m.items {
		if m.opts.FilterFunc == nil || m.opts.FilterFunc(item, m.filterActive) {
			listItems = append(listItems, listItem[T]{value: item, item: m.opts.Renderer})
		}
	}
	return m.list.SetItems(listItems)
}

// countSummary renders the item tally shown above the list.
func (m SelectionModel[T]) countSummary() string {
	countable := m.opts.CountableItem
	if countable == nil {
		countable = func(T) bool { return true }
	}

	visible := 0
	for _, listed := range m.list.VisibleItems() {
		if item, ok := listed.(listItem[T]); ok && countable(item.value) {
			visible++
		}
	}

	total := 0
	for _, item := range m.items {
		if countable(item) {
			total++
		}
	}

	name := "items"
	if visible == 1 {
		name = "item"
	}
	summary := fmt.Sprintf("%d %s", visible, name)

	if hidden := total - visible; hidden > 0 {
		summary += fmt.Sprintf(" · %d filtered", hidden)
	}
	return summary
}

// startRefresh initiates an async refresh if RefreshItems is configured and not already refreshing
func (m *SelectionModel[T]) startRefresh() (tea.Model, tea.Cmd) {
	if m.opts.RefreshItems != nil && !m.refreshing {
		m.refreshing = true
		return m, func() tea.Msg {
			items, err := m.opts.RefreshItems()
			return refreshFinishedMsg{items: items, err: err}
		}
	}
	return m, nil
}

// isSelectedResolved returns whether the currently selected item is resolved
func (m *SelectionModel[T]) isSelectedResolved() bool {
	if m.opts.IsItemResolved == nil {
		return false
	}
	selected := m.list.SelectedItem()
	if selected == nil {
		return false
	}
	item := selected.(listItem[T])
	return m.opts.IsItemResolved(item.value)
}

// getResolveActionKey returns the appropriate key for the first resolve action
func (m *SelectionModel[T]) getResolveActionKey() string {
	if m.isSelectedResolved() && m.opts.ResolveKeyAlt != "" {
		return m.opts.ResolveKeyAlt
	}
	return m.opts.ResolveKey
}

// getResolveActionKeySecond returns the appropriate key for the second resolve action
func (m *SelectionModel[T]) getResolveActionKeySecond() string {
	if m.isSelectedResolved() && m.opts.ResolveCommentKeyAlt != "" {
		return m.opts.ResolveCommentKeyAlt
	}
	return m.opts.ResolveCommentKey
}

// View renders the current model state
func (m SelectionModel[T]) View() string {
	if m.showHelp {
		return m.renderHelpOverlay()
	}

	if m.confirmationMessage != "" {
		return m.renderConfirmation()
	}

	if m.applyPreviewMode {
		return m.renderApplyPreview()
	}

	if m.showDetail {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

		// Build action hints for the sticky footer
		var actions []string
		actions = append(actions, "q/esc:back")
		if m.opts.ResolveAction != nil {
			key, _ := splitActionKey(m.getResolveActionKey())
			actions = append(actions, key+":resolve")
		}
		if m.opts.ResolveCommentPrepare != nil {
			key, _ := splitActionKey(m.getResolveActionKeySecond())
			actions = append(actions, key+":resolve+comment")
		}
		if m.opts.QuotePrepare != nil {
			key, _ := splitActionKey(m.opts.QuoteKey)
			actions = append(actions, key+":quote")
		}
		if m.opts.QuoteContextPrepare != nil {
			key, _ := splitActionKey(m.opts.QuoteContextKey)
			actions = append(actions, key+":quote+context")
		}
		if m.opts.AgentAction != nil {
			key, _ := splitActionKey(m.opts.AgentKey)
			actions = append(actions, key+":agent")
		}
		if m.opts.EditAction != nil {
			key, _ := splitActionKey(m.opts.EditKey)
			actions = append(actions, key+":edit")
		}
		if m.opts.ReactionAction != nil {
			key, _ := splitActionKey(m.opts.ReactionKey)
			actions = append(actions, key+":react")
		}
		if m.opts.ApplySuggestionAction != nil {
			key, _ := splitActionKey(m.opts.ApplySuggestionKey)
			actions = append(actions, key+":apply")
		}
		if m.opts.ApplySuggestionResolveAction != nil {
			key, _ := splitActionKey(m.opts.ApplySuggestionResolveKey)
			actions = append(actions, key+":apply+resolve")
		}
		if m.opts.OnOpen != nil {
			actions = append(actions, "o:open")
		}
		if m.opts.RefreshItems != nil {
			actions = append(actions, "i:refresh")
		}
		actions = append(actions, "ctrl+f/b:scroll")

		// Show comment selection or reaction mode status if active
		var header string
		if m.reactionMode {
			emoji := reactionEmojis[m.reactionIdx]
			reactionStatus := fmt.Sprintf("React: [%d/%d] %s (x=next, Enter=add, Esc=cancel)",
				m.reactionIdx+1, len(reactionEmojis), emoji.display)
			header = titleStyle.Render("Detail View") + "  " + helpStyle.Render(reactionStatus)
		} else if m.commentSelectMode && m.commentSelectInDetail {
			header = titleStyle.Render("Detail View") + "  " + helpStyle.Render(m.commentSelectStatus)
		} else {
			header = titleStyle.Render("Detail View") + "  " + helpStyle.Render(strings.Join(actions, " | "))
		}

		var footer string
		if m.refreshing {
			footer = helpStyle.Render("Refreshing...")
		} else {
			footer = helpStyle.Render(strings.Join(actions, " | "))
		}

		// Calculate available height for viewport
		headerHeight := lipgloss.Height(header) + 1
		footerHeight := lipgloss.Height(footer) + 1
		availableHeight := m.windowSize.Height - headerHeight - footerHeight
		if availableHeight < 1 {
			availableHeight = 1
		}

		// Update viewport size to fit available space
		m.viewport.Height = availableHeight
		m.viewport.Width = m.windowSize.Width

		return lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			m.viewport.View(),
			"",
			footer,
		)
	}

	// Build sticky footer with action hints
	var actions []string
	actions = append(actions, "enter:view")
	if m.opts.ResolveAction != nil {
		key, _ := splitActionKey(m.getResolveActionKey())
		actions = append(actions, key+":resolve")
	}
	if m.opts.ResolveCommentPrepare != nil {
		key, _ := splitActionKey(m.getResolveActionKeySecond())
		actions = append(actions, key+":resolve+comment")
	}
	if m.opts.QuotePrepare != nil {
		key, _ := splitActionKey(m.opts.QuoteKey)
		actions = append(actions, key+":quote")
	}
	if m.opts.QuoteContextPrepare != nil {
		key, _ := splitActionKey(m.opts.QuoteContextKey)
		actions = append(actions, key+":quote+context")
	}
	if m.opts.AgentAction != nil {
		key, _ := splitActionKey(m.opts.AgentKey)
		actions = append(actions, key+":agent")
	}
	if m.opts.EditAction != nil {
		key, _ := splitActionKey(m.opts.EditKey)
		actions = append(actions, key+":edit")
	}
	if m.opts.ReactionAction != nil {
		key, _ := splitActionKey(m.opts.ReactionKey)
		actions = append(actions, key+":react")
	}
	if m.opts.ApplySuggestionAction != nil {
		key, _ := splitActionKey(m.opts.ApplySuggestionKey)
		actions = append(actions, key+":apply")
	}
	if m.opts.ApplySuggestionResolveAction != nil {
		key, _ := splitActionKey(m.opts.ApplySuggestionResolveKey)
		actions = append(actions, key+":apply+resolve")
	}
	if m.opts.OnOpen != nil {
		actions = append(actions, "o:open")
	}
	if m.opts.RefreshItems != nil {
		actions = append(actions, "i:refresh")
	}
	if m.opts.FilterFunc != nil {
		actions = append(actions, "h:hide resolved")
	}
	actions = append(actions, "?:help")
	actions = append(actions, "q:quit")

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	// Show comment selection or reaction status if active
	var footer string
	if m.reactionMode {
		emoji := reactionEmojis[m.reactionIdx]
		reactionStatus := fmt.Sprintf("React: [%d/%d] %s (x=next, Enter=add, Esc=cancel)",
			m.reactionIdx+1, len(reactionEmojis), emoji.display)
		footer = helpStyle.Render(reactionStatus)
	} else if m.commentSelectMode && !m.commentSelectInDetail {
		footer = helpStyle.Render(m.commentSelectStatus)
	} else if m.refreshing {
		footer = helpStyle.Render("Refreshing...")
	} else {
		footer = helpStyle.Render(strings.Join(actions, " | "))
	}

	m.list.Title = m.countSummary()

	return lipgloss.JoinVertical(lipgloss.Left,
		m.list.View(),
		"",
		footer,
	)
}

func (m SelectionModel[T]) renderApplyPreview() string {
	height := m.windowSize.Height
	if height == 0 {
		height = 24
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	// Header
	actionText := "Apply Suggestion"
	if m.applyPreviewWithResolve {
		actionText = "Apply Suggestion + Resolve"
	}
	header := titleStyle.Render(actionText) + "  " + helpStyle.Render("Press 'y' to apply, any other key to cancel")

	// Footer
	footer := helpStyle.Render("y:apply | any other key:cancel")

	// Colorize the diff
	coloredDiff := ColorizeDiff(m.applyPreviewDiff)

	// Calculate available height for diff
	headerHeight := lipgloss.Height(header) + 1
	footerHeight := lipgloss.Height(footer) + 1
	availableHeight := height - headerHeight - footerHeight - 2

	// Truncate if too long
	diffLines := strings.Split(coloredDiff, "\n")
	if availableHeight < 2 {
		availableHeight = 2
	}
	if len(diffLines) > availableHeight {
		diffLines = diffLines[:availableHeight-1]
		diffLines = append(diffLines, helpStyle.Render("... (diff truncated)"))
	}
	displayDiff := strings.Join(diffLines, "\n")

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		displayDiff,
		"",
		footer,
	)
}

// renderConfirmation renders a centered confirmation dialog
func (m SelectionModel[T]) renderConfirmation() string {
	width := m.windowSize.Width
	height := m.windowSize.Height

	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	// Create styled box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Width(60)

	box := boxStyle.Render(m.confirmationMessage)

	// Center the box
	boxHeight := lipgloss.Height(box)
	boxWidth := lipgloss.Width(box)

	topPadding := (height - boxHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}
	leftPadding := (width - boxWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}

	// Build the centered view
	var lines []string
	for i := 0; i < topPadding; i++ {
		lines = append(lines, "")
	}

	// Add left padding to each line of the box
	for _, line := range strings.Split(box, "\n") {
		lines = append(lines, strings.Repeat(" ", leftPadding)+line)
	}

	return strings.Join(lines, "\n")
}

// renderHelpOverlay renders a help overlay
func (m SelectionModel[T]) renderHelpOverlay() string {
	width := m.windowSize.Width
	height := m.windowSize.Height

	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	helpText := `Keyboard Shortcuts

Navigation:
  ↑/↓, j/k     Move up/down
  enter, l, →  View detail / select
  ←, esc       Go back (from detail)
  q            Quit (list) / Back (detail)
  /            Filter items
  h            Toggle hide resolved (list)

Actions:`

	// Add dynamic action help
	if m.opts.ResolveAction != nil {
		key, desc := splitActionKey(m.getResolveActionKey())
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.ResolveCommentPrepare != nil {
		key, desc := splitActionKey(m.getResolveActionKeySecond())
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.QuotePrepare != nil {
		key, desc := splitActionKey(m.opts.QuoteKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.QuoteContextPrepare != nil {
		key, desc := splitActionKey(m.opts.QuoteContextKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.AgentAction != nil {
		key, desc := splitActionKey(m.opts.AgentKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.EditAction != nil {
		key, desc := splitActionKey(m.opts.EditKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.ReactionAction != nil {
		key, desc := splitActionKey(m.opts.ReactionKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.ApplySuggestionAction != nil {
		key, desc := splitActionKey(m.opts.ApplySuggestionKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.ApplySuggestionResolveAction != nil {
		key, desc := splitActionKey(m.opts.ApplySuggestionResolveKey)
		helpText += fmt.Sprintf("\n  %-12s %s", key, desc)
	}
	if m.opts.OnOpen != nil {
		helpText += fmt.Sprintf("\n  %-12s %s", "o", "open in browser")
	}
	if m.opts.RefreshItems != nil {
		helpText += fmt.Sprintf("\n  %-12s %s", "i", "refresh")
	}

	helpText += `

Detail View:
  i            Refresh content
  ctrl+f       Page down
  ctrl+b       Page up

Press any key to close this help...`

	// Create styled box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2)

	box := boxStyle.Render(helpText)

	// Center the box
	boxHeight := lipgloss.Height(box)
	boxWidth := lipgloss.Width(box)

	topPadding := (height - boxHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}
	leftPadding := (width - boxWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}

	// Build the centered view
	var lines []string
	for i := 0; i < topPadding; i++ {
		lines = append(lines, "")
	}

	// Add left padding to each line of the box
	for _, line := range strings.Split(box, "\n") {
		lines = append(lines, strings.Repeat(" ", leftPadding)+line)
	}

	return strings.Join(lines, "\n")
}

// itemDelegate renders individual list items
type itemDelegate[T any] struct {
	renderer ItemRenderer[T]
}

func (d itemDelegate[T]) Height() int {
	return 1
}

func (d itemDelegate[T]) Spacing() int {
	return 0
}

func (d itemDelegate[T]) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return nil
}

func (d itemDelegate[T]) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(listItem[T])
	if !ok {
		return
	}

	// Check if item should be skipped (visually marked)
	isSkippable := d.renderer.IsSkippable(i.value)

	title := i.Title()
	desc := i.Description()

	// Truncate on visible columns. ANSI escapes occupy bytes but no
	// columns, so a byte-based cut both undercounts the room available and
	// can slice through an escape sequence.
	maxWidth := m.Width() - 4
	if maxWidth > 0 {
		title = ansi.Truncate(title, maxWidth, "...")
		desc = ansi.Truncate(desc, maxWidth, "...")
	}

	var line string
	if desc != "" {
		line = fmt.Sprintf("%s - %s", title, desc)
	} else {
		line = title
	}

	// Style based on selection and skippable state
	if index == m.Index() {
		style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
		if isSkippable {
			style = style.Strikethrough(true).Foreground(lipgloss.Color("241"))
		}
		line = style.Render("> " + line)
	} else {
		if isSkippable {
			line = lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("241")).Render("  " + line)
		} else {
			line = "  " + line
		}
	}

	_, _ = fmt.Fprint(w, line)
}

// enterCommentSelectMode starts comment selection for the given action
func (m *SelectionModel[T]) enterCommentSelectMode(action string, item listItem[T]) {
	m.commentSelectMode = true
	m.commentSelectAction = action
	m.commentSelectIdx = 0
	m.commentSelectItem = item
	m.commentSelectInDetail = false

	// Build initial status
	count := m.opts.Renderer.ThreadCommentCount(item.value)
	preview := m.opts.Renderer.ThreadCommentPreview(item.value, 0)
	m.commentSelectStatus = fmt.Sprintf("[1/%d] %s (%s=next, Enter=select, Esc=cancel)", count, preview, action)
}

// cycleCommentSelection advances to the next comment in the thread
func (m *SelectionModel[T]) cycleCommentSelection() {
	count := m.opts.Renderer.ThreadCommentCount(m.commentSelectItem.value)
	m.commentSelectIdx = (m.commentSelectIdx + 1) % count

	// Update status
	preview := m.opts.Renderer.ThreadCommentPreview(m.commentSelectItem.value, m.commentSelectIdx)
	m.commentSelectStatus = fmt.Sprintf("[%d/%d] %s (%s=next, Enter=select, Esc=cancel)",
		m.commentSelectIdx+1, count, preview, m.commentSelectAction)
}

// showCommentSelectStatus returns a command to display the current selection status
func (m *SelectionModel[T]) showCommentSelectStatus() tea.Cmd {
	return m.list.NewStatusMessage(m.commentSelectStatus)
}

// exitCommentSelectMode clears the comment selection state
func (m *SelectionModel[T]) exitCommentSelectMode() {
	m.commentSelectMode = false
	m.commentSelectAction = ""
	m.commentSelectIdx = 0
	m.commentSelectInDetail = false
	m.commentSelectStatus = ""
}

// handleQuoteKey handles the 'Q' key for quote reply, used by both list and detail views
func (m *SelectionModel[T]) handleQuoteKey(inDetailView bool) (tea.Model, tea.Cmd) {
	if m.opts.QuotePrepare == nil {
		return m, nil
	}
	selected := m.list.SelectedItem()
	if selected == nil {
		return m, nil
	}

	item := selected.(listItem[T])
	count := m.opts.Renderer.ThreadCommentCount(item.value)

	if count > 1 {
		m.enterCommentSelectMode("Q", item)
		if inDetailView {
			m.commentSelectInDetail = true
			m.updateDetailViewWithHighlight()
		}
		return m, m.showCommentSelectStatus()
	}

	if inDetailView {
		m.showDetail = false
	}
	return m, m.startEditorForAction(item.value, 3)
}

// handleQuoteContextKey handles the 'C' key for quote with context, used by both list and detail views
func (m *SelectionModel[T]) handleQuoteContextKey(inDetailView bool) (tea.Model, tea.Cmd) {
	if m.opts.QuoteContextPrepare == nil {
		return m, nil
	}
	selected := m.list.SelectedItem()
	if selected == nil {
		return m, nil
	}

	item := selected.(listItem[T])
	count := m.opts.Renderer.ThreadCommentCount(item.value)

	if count > 1 {
		m.enterCommentSelectMode("C", item)
		if inDetailView {
			m.commentSelectInDetail = true
			m.updateDetailViewWithHighlight()
		}
		return m, m.showCommentSelectStatus()
	}

	if inDetailView {
		m.showDetail = false
	}
	return m, m.startEditorForAction(item.value, 4)
}

// handleAgentKey handles the 'a' key for agent action, used by both list and detail views
func (m *SelectionModel[T]) handleAgentKey(inDetailView bool) (tea.Model, tea.Cmd) {
	if m.opts.AgentAction == nil {
		return m, nil
	}
	selected := m.list.SelectedItem()
	if selected == nil {
		return m, nil
	}

	item := selected.(listItem[T])
	count := m.opts.Renderer.ThreadCommentCount(item.value)

	if count > 1 {
		m.enterCommentSelectMode("a", item)
		if inDetailView {
			m.commentSelectInDetail = true
			m.updateDetailViewWithHighlight()
		}
		return m, m.showCommentSelectStatus()
	}

	result, err := m.opts.AgentAction(item.value)
	if err != nil {
		return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
	}
	if strings.HasPrefix(result, "LAUNCH_AGENT:") {
		prompt := strings.TrimPrefix(result, "LAUNCH_AGENT:")
		if inDetailView {
			m.showDetail = false
		}
		return m, m.launchAgent(prompt)
	}
	if result != "" {
		return m, m.list.NewStatusMessage(result)
	}
	return m, nil
}

// handleReactionKey handles the 'x' key to add reactions, used by both list and detail views
func (m *SelectionModel[T]) handleReactionKey(inDetailView bool) (tea.Model, tea.Cmd) {
	if m.opts.ReactionAction == nil {
		return m, nil
	}
	selected := m.list.SelectedItem()
	if selected == nil {
		return m, nil
	}

	item := selected.(listItem[T])
	count := m.opts.Renderer.ThreadCommentCount(item.value)

	if count > 1 && (!inDetailView || (inDetailView && !m.commentSelectMode)) {
		m.enterCommentSelectMode("x", item)
		if inDetailView {
			m.commentSelectInDetail = true
			m.updateDetailViewWithHighlight()
		}
		return m, m.showCommentSelectStatus()
	}

	commentID, err := m.opts.ReactionAction(item.value)
	if err != nil {
		return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
	}
	m.enterReactionMode(commentID, item)
	return m, m.showReactionStatus()
}

// enterReactionMode starts reaction selection for the given comment
func (m *SelectionModel[T]) enterReactionMode(commentID int64, item listItem[T]) {
	m.reactionMode = true
	m.reactionIdx = 0
	m.reactionCommentID = commentID
	m.reactionItem = item
}

// showReactionStatus returns a command to display the current reaction selection status
func (m *SelectionModel[T]) showReactionStatus() tea.Cmd {
	emoji := reactionEmojis[m.reactionIdx]
	msg := fmt.Sprintf("React: [%d/%d] %s (x=next, Enter=add, Esc=cancel)",
		m.reactionIdx+1, len(reactionEmojis), emoji.display)
	return m.list.NewStatusMessage(msg)
}

// updateDetailViewWithHighlight updates the detail view to highlight the currently selected comment
func (m *SelectionModel[T]) updateDetailViewWithHighlight() {
	if !m.showDetail {
		return
	}
	content := m.opts.Renderer.PreviewWithHighlight(m.commentSelectItem.value, m.commentSelectIdx)
	m.viewport.SetContent(content)

	// Scroll to make the highlighted section visible
	// Look for the highlight marker and scroll to it
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, "SELECTED") {
			// Set Y offset so the highlighted line is near the top of the viewport
			// Leave a few lines of context above if possible
			offset := i - 2
			if offset < 0 {
				offset = 0
			}
			m.viewport.SetYOffset(offset)
			break
		}
	}
}

// executeCommentAction runs the pending action with the selected comment
func (m *SelectionModel[T]) executeCommentAction() (tea.Model, tea.Cmd) {
	// Update item with selected comment index
	itemWithSelection := m.opts.Renderer.WithSelectedComment(m.commentSelectItem.value, m.commentSelectIdx)
	m.commentSelectItem.value = itemWithSelection

	action := m.commentSelectAction
	wasInDetail := m.commentSelectInDetail
	m.exitCommentSelectMode()

	switch action {
	case "Q":
		if wasInDetail {
			m.showDetail = false
		}
		return m, m.startEditorForAction(itemWithSelection, 3)
	case "C":
		if wasInDetail {
			m.showDetail = false
		}
		return m, m.startEditorForAction(itemWithSelection, 4)
	case "a":
		if m.opts.AgentAction != nil {
			result, err := m.opts.AgentAction(itemWithSelection)
			if err != nil {
				return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
			}
			if strings.HasPrefix(result, "LAUNCH_AGENT:") {
				prompt := strings.TrimPrefix(result, "LAUNCH_AGENT:")
				if wasInDetail {
					m.showDetail = false
				}
				return m, m.launchAgent(prompt)
			}
			if result != "" {
				return m, m.list.NewStatusMessage(result)
			}
		}
	case "x":
		if m.opts.ReactionAction != nil {
			commentID, err := m.opts.ReactionAction(itemWithSelection)
			if err != nil {
				return m, m.list.NewStatusMessage(Colorize(ColorRed, err.Error()))
			}
			// Create a listItem with the updated selection
			itemWithIdx := listItem[T]{value: itemWithSelection, item: m.opts.Renderer}
			m.enterReactionMode(commentID, itemWithIdx)
			return m, m.showReactionStatus()
		}
	}

	return m, nil
}
