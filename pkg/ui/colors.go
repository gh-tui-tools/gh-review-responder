package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/termenv"
)

const (
	ColorReset   = "\033[0m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorGray    = "\033[90m"
)

var (
	colorEnabled = true
	uiDebug      atomic.Bool
)

// SetUIDebug enables debug timing output for UI operations.
func SetUIDebug(enabled bool) {
	uiDebug.Store(enabled)
}

// Cached glamour renderer for markdown rendering (created once, reused)
var (
	cachedMarkdownRenderer *glamour.TermRenderer
	rendererInitOnce       sync.Once
)

// Pre-compiled regexes for StripSuggestionBlock (avoids recompilation on each call)
var (
	suggestionBlockRe = regexp.MustCompile("(?s)```suggestion\\s*\\n.*?```")
	imageMarkdownRe   = regexp.MustCompile(`!\[.*?\]\(.*?\)`)
)

// WarmupMarkdownRenderer initializes the markdown renderer and warms up the
// syntax highlighting system in the background. Call this early in the app
// lifecycle to avoid delays on first render.
func WarmupMarkdownRenderer() {
	go func() {
		start := time.Now()
		// Initialize the renderer (this creates glamour's TermRenderer)
		r := getMarkdownRenderer()
		if r != nil {
			// Warm up chroma's lexers by rendering some code blocks
			// This triggers lazy initialization of syntax highlighters
			_, _ = r.Render("```go\nfunc main() {}\n```")
			_, _ = r.Render("```js\nconst x = 1;\n```")
		}
		if uiDebug.Load() {
			fmt.Fprintf(os.Stderr, "[DEBUG] Markdown warmup completed in %v\n", time.Since(start))
		}
	}()
}

// SetColorEnabled toggles ANSI color output across the UI helpers.
// Must be called before any rendering occurs (typically at startup).
func SetColorEnabled(enabled bool) {
	colorEnabled = enabled
	if !enabled {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// ColorsEnabled reports whether ANSI colors are enabled.
func ColorsEnabled() bool {
	return colorEnabled
}

// EmojiText returns emojiText when colors/emoji are enabled, otherwise the plain fallback.
func EmojiText(emojiText, plainText string) string {
	if !colorEnabled {
		return plainText
	}
	return emojiText
}

// Colorize applies ANSI color codes to text
func Colorize(color, text string) string {
	if !colorEnabled {
		return text
	}
	return color + text + ColorReset
}

// FormatDiffWithHeaders prepends git-style --- a/ and +++ b/ headers to a diff hunk.
// This is useful for displaying diff context with file path information.
func FormatDiffWithHeaders(diffHunk, path string) string {
	if path == "" {
		return diffHunk
	}
	return fmt.Sprintf("--- a/%s\n+++ b/%s\n%s", path, path, diffHunk)
}

// TruncateDiff limits a diff to maxLines, appending "..." if truncated.
// If maxLines is 0 or negative, no truncation is applied.
func TruncateDiff(diff string, maxLines int) string {
	if maxLines <= 0 {
		return diff
	}
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

// TruncateDiffTail returns the last maxLines lines of a diff, keeping the
// lines closest to the comment. If the diff is already short enough, it is
// returned unchanged.
func TruncateDiffTail(diff string, maxLines int) string {
	if maxLines <= 0 {
		return diff
	}
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	return "...\n" + strings.Join(lines[len(lines)-maxLines:], "\n")
}

// ColorizeDiff applies syntax highlighting to diff hunks
func ColorizeDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	var coloredLines []string

	for _, line := range lines {
		if len(line) == 0 {
			coloredLines = append(coloredLines, line)
			continue
		}

		switch line[0] {
		case '+':
			coloredLines = append(coloredLines, Colorize(ColorGreen, line))
		case '-':
			coloredLines = append(coloredLines, Colorize(ColorRed, line))
		case '@':
			coloredLines = append(coloredLines, Colorize(ColorCyan, line))
		default:
			coloredLines = append(coloredLines, Colorize(ColorGray, line))
		}
	}

	return strings.Join(coloredLines, "\n")
}

// ColorizeCode applies syntax highlighting to suggested code
func ColorizeCode(code string) string {
	return Colorize(ColorGreen, code)
}

// CreateHyperlink creates an OSC8 hyperlink
func CreateHyperlink(url, text string) string {
	if !colorEnabled {
		return text
	}
	if url == "" {
		return text
	}
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// StripSuggestionBlock removes the suggestion code block and images from comment body
func StripSuggestionBlock(body string) string {
	result := strings.TrimSpace(body)

	// Remove ```suggestion...``` blocks
	result = suggestionBlockRe.ReplaceAllString(result, "")

	// Remove markdown image links like ![alt](url)
	result = imageMarkdownRe.ReplaceAllString(result, "")

	return strings.TrimSpace(result)
}

// WrapText wraps text to a maximum line width
func WrapText(text string, width int) string {
	return wordwrap.String(text, width)
}

// getMarkdownRenderer returns a cached glamour renderer, creating it once if needed
func getMarkdownRenderer() *glamour.TermRenderer {
	rendererInitOnce.Do(func() {
		var start time.Time
		if uiDebug.Load() {
			start = time.Now()
			fmt.Fprintf(os.Stderr, "[DEBUG] Creating glamour renderer...\n")
		}
		// Create renderer once and cache it
		// Use dark style directly instead of WithAutoStyle() which can be slow
		// due to terminal capability detection
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(80),
		)
		if err == nil {
			cachedMarkdownRenderer = r
		}
		if uiDebug.Load() {
			fmt.Fprintf(os.Stderr, "[DEBUG] Glamour renderer created in %v\n", time.Since(start))
		}
	})
	return cachedMarkdownRenderer
}

// RenderMarkdown renders markdown text with glamour
func RenderMarkdown(text string) (string, error) {
	if text == "" {
		return "", nil
	}

	if !colorEnabled {
		return strings.TrimSpace(text), nil
	}

	r := getMarkdownRenderer()
	if r == nil {
		// Fallback to plain text if renderer creation failed
		return text, nil
	}

	var start time.Time
	if uiDebug.Load() {
		start = time.Now()
	}

	rendered, err := r.Render(text)

	if uiDebug.Load() {
		fmt.Fprintf(os.Stderr, "[DEBUG] RenderMarkdown took %v for %d bytes\n", time.Since(start), len(text))
	}

	if err != nil {
		// Fallback to plain text if rendering fails
		return text, nil
	}

	return strings.TrimSpace(rendered), nil
}

// ============================================================================
// Author Styling
// ============================================================================

// AuthorStyle represents styling information for a GitHub author.
type AuthorStyle struct {
	Name  string // Author name (without @ symbol)
	IsBot bool   // True if author name ends with [bot]
	Color string // ANSI color code (cyan for users, yellow for bots)
}

// NewAuthorStyle creates a new author style based on the author name.
// Bots (ending with [bot]) are colored yellow, regular users in cyan.
func NewAuthorStyle(author string) *AuthorStyle {
	isBot := strings.HasSuffix(author, "[bot]") || strings.EqualFold(author, "Copilot")
	name := author
	if strings.HasSuffix(author, "[bot]") {
		name = strings.TrimSuffix(author, "[bot]")
	}

	style := &AuthorStyle{
		Name:  name,
		IsBot: isBot,
	}

	if style.IsBot {
		style.Color = ColorYellow
	} else {
		style.Color = ColorCyan
	}

	return style
}

// Format returns the formatted author string with color (colored "@authorname").
func (as *AuthorStyle) Format(includeIcon bool) string {
	if includeIcon {
		icon := EmojiText("👤", "")
		if as.IsBot {
			icon = EmojiText("🤖", "")
		}
		if icon != "" {
			return fmt.Sprintf("%s %s", icon, Colorize(as.Color, "@"+as.Name))
		}
	}
	return Colorize(as.Color, "@"+as.Name)
}

// ============================================================================
// Status Styling
// ============================================================================

// StatusStyle represents styling information for resolved/unresolved status.
type StatusStyle struct {
	IsResolved bool   // True if resolved, false if unresolved
	Label      string // "resolved" or "unresolved"
	Color      string // ANSI color code (green for resolved, yellow for unresolved)
	Emoji      string // Visual indicator (✅ or ⚠️)
}

// NewStatusStyle creates a new status style for the given resolved state.
func NewStatusStyle(isResolved bool) *StatusStyle {
	style := &StatusStyle{
		IsResolved: isResolved,
	}

	if isResolved {
		style.Label = "resolved"
		style.Color = ColorGreen
		style.Emoji = "✅"
	} else {
		style.Label = "unresolved"
		style.Color = ColorYellow
		style.Emoji = "⚠️ " // Extra space after ⚠️ for better visual spacing
	}

	return style
}

// Format returns the formatted status string with color and emoji.
// When includeEmoji is true, the emoji indicator is prepended to the status.
func (ss *StatusStyle) Format(includeEmoji bool) string {
	if includeEmoji {
		emoji := EmojiText(ss.Emoji, "")
		if emoji != "" {
			return fmt.Sprintf("%s %s", emoji, Colorize(ss.Color, ss.Label))
		}
	}
	return Colorize(ss.Color, ss.Label)
}

// ============================================================================
// Review List Item Styling
// ============================================================================

// ReviewListStyle provides formatting for list items showing review comments or suggestions.
type ReviewListStyle struct {
	Author *AuthorStyle // Author styling (with bot detection)
	Status *StatusStyle // Resolution status styling
}

// NewReviewListStyle creates a new review list style from comment data.
func NewReviewListStyle(authorName string, isResolved bool) *ReviewListStyle {
	return &ReviewListStyle{
		Author: NewAuthorStyle(authorName),
		Status: NewStatusStyle(isResolved),
	}
}

// CommentListStyle is an alias for backward compatibility with resolve.go.
type CommentListStyle = ReviewListStyle

// NewCommentListStyle creates a new comment list style from comment data.
// Deprecated: Use NewReviewListStyle instead.
func NewCommentListStyle(authorName string, isResolved bool) *CommentListStyle {
	return NewReviewListStyle(authorName, isResolved)
}

// SuggestionListStyle is an alias for backward compatibility with applier.go.
type SuggestionListStyle = ReviewListStyle

// NewSuggestionListStyle creates a new suggestion list style from comment data.
// Deprecated: Use NewReviewListStyle instead.
func NewSuggestionListStyle(authorName string, isResolved bool) *SuggestionListStyle {
	return NewReviewListStyle(authorName, isResolved)
}

// FormatCommentTitle returns a formatted title for comment list display: "@author".
func (rls *ReviewListStyle) FormatCommentTitle(commentID int64) string {
	return rls.Author.Format(false)
}

// FormatCommentDescription returns a formatted description for comment list: "file:line [emoji status]".
func (rls *ReviewListStyle) FormatCommentDescription(filePath string, lineNumber int) string {
	return fmt.Sprintf("%s:%d %s", filePath, lineNumber, rls.Status.Format(true))
}

// FormatSuggestionTitle returns a formatted title for suggestion list: "@author • file:line".
func (rls *ReviewListStyle) FormatSuggestionTitle(filePath string, lineNumber int) string {
	return fmt.Sprintf("%s • %s:%d", rls.Author.Format(false), filePath, lineNumber)
}

// FormatSuggestionDescription returns a formatted description with status and tags for suggestion list.
func (rls *ReviewListStyle) FormatSuggestionDescription(hasSuggestion bool, isOutdated bool) string {
	var parts []string

	if hasSuggestion {
		parts = append(parts, "[suggestion]")
	}

	if isOutdated {
		parts = append(parts, Colorize(ColorYellow, EmojiText("⚠️ OUTDATED", "OUTDATED")))
	}

	parts = append(parts, rls.Status.Format(true))

	return strings.Join(parts, " ")
}

// FormatRelativeTime formats a time as a human-readable relative string
// like "5 minutes ago", "2 hours ago", "3 days ago", etc.
func FormatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 30*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case diff < 365*24*time.Hour:
		months := int(diff.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(diff.Hours() / 24 / 365)
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// ReactionCounts holds counts for each reaction type for display purposes
type ReactionCounts struct {
	PlusOne  int
	MinusOne int
	Laugh    int
	Hooray   int
	Confused int
	Heart    int
	Rocket   int
	Eyes     int
}

// ReactionCountsFromGitHub converts a github.Reactions to a ReactionCounts.
func ReactionCountsFromGitHub(r github.Reactions) ReactionCounts {
	return ReactionCounts{
		PlusOne:  r.PlusOne,
		MinusOne: r.MinusOne,
		Laugh:    r.Laugh,
		Hooray:   r.Hooray,
		Confused: r.Confused,
		Heart:    r.Heart,
		Rocket:   r.Rocket,
		Eyes:     r.Eyes,
	}
}

// FormatReactions formats reaction counts for display with actual emoji characters.
// Returns an empty string if there are no reactions.
func FormatReactions(r ReactionCounts) string {
	var parts []string

	// Order matches GitHub's display order
	if r.PlusOne > 0 {
		parts = append(parts, fmt.Sprintf("👍 %d", r.PlusOne))
	}
	if r.MinusOne > 0 {
		parts = append(parts, fmt.Sprintf("👎 %d", r.MinusOne))
	}
	if r.Laugh > 0 {
		parts = append(parts, fmt.Sprintf("😄 %d", r.Laugh))
	}
	if r.Hooray > 0 {
		parts = append(parts, fmt.Sprintf("🎉 %d", r.Hooray))
	}
	if r.Confused > 0 {
		parts = append(parts, fmt.Sprintf("😕 %d", r.Confused))
	}
	if r.Heart > 0 {
		parts = append(parts, fmt.Sprintf("❤️ %d", r.Heart))
	}
	if r.Rocket > 0 {
		parts = append(parts, fmt.Sprintf("🚀 %d", r.Rocket))
	}
	if r.Eyes > 0 {
		parts = append(parts, fmt.Sprintf("👀 %d", r.Eyes))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, " ")
}
