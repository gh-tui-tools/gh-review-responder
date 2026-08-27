package ui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ItemRenderer defines how to render an item in the selector
type ItemRenderer[T any] interface {
	// Title returns the primary display text for an item
	Title(item T) string
	// Description returns secondary text for an item
	Description(item T) string
	// Preview returns detailed preview text for an item
	Preview(item T) string
	// PreviewWithHighlight returns detailed preview text with a specific comment highlighted
	// highlightIdx: 0 = main comment, 1+ = thread replies. -1 = no highlight.
	PreviewWithHighlight(item T, highlightIdx int) string
	// EditPath returns the file path to open in editor (optional)
	EditPath(item T) string
	// EditLine returns the line number to go to in editor (1-based, optional)
	EditLine(item T) int
	// FilterValue returns the string to match against when filtering
	FilterValue(item T) string
	// IsSkippable returns true if the item should be skipped during navigation
	IsSkippable(item T) bool
	// ThreadCommentCount returns the number of comments in this item's thread
	// (1 = main only, >1 = main + replies). Return 0 if not applicable.
	ThreadCommentCount(item T) int
	// ThreadCommentPreview returns a preview string for the comment at index
	// (0 = main comment, 1+ = thread replies)
	ThreadCommentPreview(item T, idx int) string
	// WithSelectedComment returns a copy of item with the selected comment index set
	WithSelectedComment(item T, idx int) T
}

// CustomAction is a function that handles custom actions on items
type CustomAction[T any] func(item T) (string, error)

// EditorPreparer returns the initial content for the editor, or error to abort
type EditorPreparer[T any] func(item T) (string, error)

// EditorCompleter is called with the editor content to complete the action
type EditorCompleter[T any] func(item T, editorContent string) (string, error)

// editorFinishedMsg is sent when an editor process completes
type editorFinishedMsg struct {
	err error
}

// loadDetailMsg triggers the actual detail loading after showing loading state
type loadDetailMsg struct{}

// refreshFinishedMsg signals that refresh has completed
type refreshFinishedMsg struct {
	items any // will be []T
	err   error
}

// agentFinishedMsg is sent when the coding agent process completes
type agentFinishedMsg struct {
	err error
}

// ErrNoSelection is returned when no item was selected
var ErrNoSelection = errors.New("no selection made")

// SelectorOptions configures the interactive selector.
// Use this struct to configure all selector behavior in a readable way.
type SelectorOptions[T any] struct {
	// Required
	Items    []T
	Renderer ItemRenderer[T]

	// Core callbacks
	OnSelect       CustomAction[T]     // Called when Enter is pressed
	OnOpen         CustomAction[T]     // Called when 'o' is pressed
	FilterFunc     func(T, bool) bool  // Filter items based on state
	FilterDefault  bool                // Initial filter state (true = filter active, e.g., hide resolved)
	IsItemResolved func(T) bool        // For dynamic key display (r vs u)
	RefreshItems   func() ([]T, error) // Called when 'i' is pressed

	// CountableItem reports whether an item counts toward the tally shown
	// above the list. Rows that exist only for layout, such as group headers
	// and preview rows, return false so the tally reflects real entries.
	// When nil, every visible row counts.
	CountableItem func(T) bool

	// Action: r/u (resolve toggle)
	ResolveAction CustomAction[T]
	ResolveKey    string // e.g., "r resolve"
	ResolveKeyAlt string // e.g., "u unresolve"

	// Action: R/U (resolve+comment via editor)
	ResolveCommentPrepare  EditorPreparer[T]
	ResolveCommentComplete EditorCompleter[T]
	ResolveCommentKey      string // e.g., "R resolve+comment"
	ResolveCommentKeyAlt   string // e.g., "U unresolve+comment"

	// Action: Q (quote reply via editor)
	QuotePrepare  EditorPreparer[T]
	QuoteComplete EditorCompleter[T]
	QuoteKey      string // e.g., "Q quote"

	// Action: C (quote+context via editor)
	QuoteContextPrepare  EditorPreparer[T]
	QuoteContextComplete EditorCompleter[T]
	QuoteContextKey      string // e.g., "C quote+context"

	// Action: a (launch agent)
	AgentAction CustomAction[T]
	AgentKey    string // e.g., "a agent"

	// Action: e (edit file)
	EditAction CustomAction[T]
	EditKey    string // e.g., "e edit"

	// Action: x (add reaction)
	ReactionAction   func(T) (int64, error)                                                      // Returns comment ID to react to
	ReactionComplete func(item T, commentID int64, apiName, displayEmoji string) (string, error) // Applies reaction, returns confirmation message
	ReactionKey      string                                                                      // e.g., "x react"

	// Action: s (apply suggestion)
	ApplySuggestionPreview CustomAction[T] // Returns diff preview string
	ApplySuggestionAction  CustomAction[T] // Actually applies the suggestion
	ApplySuggestionKey     string          // e.g., "s apply"

	// Action: S (apply suggestion and resolve)
	ApplySuggestionResolveAction CustomAction[T]
	ApplySuggestionResolveKey    string // e.g., "S apply+resolve"
}

// SelectionModel is the tea.Model for interactive selection
type SelectionModel[T any] struct {
	list       list.Model
	items      []T
	result     []T
	windowSize tea.WindowSizeMsg
	viewport   viewport.Model
	showDetail bool
	showHelp   bool

	// Configuration (from SelectorOptions)
	opts         SelectorOptions[T]
	filterActive bool

	// Runtime state for refresh
	refreshing bool

	// State for pending editor operation
	pendingEditorItem    T
	pendingEditorTmpFile string
	pendingEditorAction  int // 2 = R/U, 3 = Q, 4 = C

	// Confirmation message that persists until user dismisses it
	confirmationMessage string

	// Loading state for detail view
	loadingDetail bool

	// Comment selection mode state (for cycling through thread comments)
	commentSelectMode     bool        // true when cycling through comments
	commentSelectAction   string      // "Q", "C", or "a" - which action triggered selection
	commentSelectIdx      int         // current index (0 = main, 1+ = thread replies)
	commentSelectItem     listItem[T] // the item being operated on
	commentSelectStatus   string      // status message to display during selection
	commentSelectInDetail bool        // true if selection was triggered from detail view

	// Reaction mode state (for cycling through emoji reactions)
	reactionMode      bool        // true when cycling through reactions
	reactionIdx       int         // current emoji index (0-7)
	reactionCommentID int64       // comment ID to react to
	reactionItem      listItem[T] // the item being reacted to

	// Apply suggestion preview state
	applyPreviewMode        bool        // true when showing apply preview
	applyPreviewDiff        string      // the diff to show
	applyPreviewItem        listItem[T] // the item being applied
	applyPreviewWithResolve bool        // true if should also resolve after applying
}

// listItem wraps a generic item for the list model
type listItem[T any] struct {
	value T
	item  ItemRenderer[T]
}

func (i listItem[T]) FilterValue() string {
	return i.item.FilterValue(i.value)
}

func (i listItem[T]) Title() string {
	return i.item.Title(i.value)
}

func (i listItem[T]) Description() string {
	return i.item.Description(i.value)
}

// SanitizeEditorContent strips trailing lines starting with # and trims whitespace.
// This preserves Markdown headings in the body while removing the instruction
// template that is appended at the end of editor content.
func SanitizeEditorContent(raw string) string {
	lines := strings.Split(raw, "\n")
	// Remove trailing lines that are empty or start with #
	for len(lines) > 0 {
		last := lines[len(lines)-1]
		if strings.TrimSpace(last) == "" || strings.HasPrefix(last, "#") {
			lines = lines[:len(lines)-1]
		} else {
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// splitActionKey splits an action key like "r resolve" into key and description
func splitActionKey(actionKey string) (string, string) {
	parts := strings.Fields(actionKey)
	if len(parts) == 0 {
		return actionKey, ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}
