package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/gh-tui-tools/gh-review-responder/pkg/applier"
	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
	"github.com/spf13/cobra"
)

// Pre-compiled regexes for markdown stripping (avoids recompilation on each call)
var (
	markdownImageRe = regexp.MustCompile(`!\[.*?\]\(.*?\)`)
	markdownLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

	// coderabbitSeverityRe matches the header line CodeRabbit puts at the top
	// of a review comment: three underscore-delimited segments separated by
	// pipes, the second of which carries the severity.
	coderabbitSeverityRe = regexp.MustCompile(`^_[^_]+_\s*\|\s*_([^_]+)_\s*\|`)

	// coderabbitTitleRe matches a bold span at the start of a line. Anchoring
	// to the line start keeps it clear of the bold markers that turn up inside
	// the shell snippets CodeRabbit embeds in its collapsed analysis blocks.
	coderabbitTitleRe = regexp.MustCompile(`^\s*\*\*(.+?)\*\*`)

	// severityLeadInRe matches the emoji and spacing ahead of a severity word.
	severityLeadInRe = regexp.MustCompile(`^[^\p{L}\p{N}]+`)
)

var browseDebug bool

var browseCmd = &cobra.Command{
	Use:   "browse [PR_NUMBER] [COMMENT_ID]",
	Short: "Browse and open review comments in your browser",
	Long: `Browse and open GitHub pull request review comments in your default browser.

When no arguments are provided, PR is inferred from the current branch and you can interactively select a comment.
When one argument is provided, it's treated as COMMENT_ID and PR is inferred from the current branch.
When two arguments are provided, the first is PR_NUMBER and the second is COMMENT_ID.`,
	Args: cobra.MaximumNArgs(2),
	RunE: runBrowse,
}

func init() {
	browseCmd.Flags().BoolVar(&browseDebug, "debug", false, "Enable debug output")
}

func runBrowse(cmd *cobra.Command, args []string) error {
	// Enable UI debug output if requested
	ui.SetUIDebug(browseDebug)

	// Start warming up the markdown renderer in the background
	// This initializes glamour/chroma before the user needs it
	ui.WarmupMarkdownRenderer()

	client := github.NewClient()
	client.SetDebug(browseDebug)
	if repoFlag != "" {
		client.SetRepo(repoFlag)
	}

	var prNumber int
	var commentID int64
	var err error

	// Parse arguments based on count
	if len(args) == 0 {
		// No args: infer PR and let user select a comment interactively
		prNumber, err = getPRNumberWithSelection([]string{}, client)
		if err != nil {
			return err
		}

		comments, err := client.FetchReviewComments(prNumber)
		if err != nil {
			return fmt.Errorf("failed to fetch review comments: %w", err)
		}
		if len(comments) == 0 {
			fmt.Printf("No review comments found in %s\n",
				ui.CreateHyperlink(fmt.Sprintf("https://github.com/%s/pull/%d", getRepoFromClient(client), prNumber),
					ui.Colorize(ui.ColorCyan, fmt.Sprintf("PR #%d", prNumber))))
			return nil
		}

		// Track collapsed state
		collapsedFiles := make(map[string]bool)

		// Use interactive selector with resolve action
		renderer := &browseItemRenderer{
			repo:           getRepoFromClient(client),
			prNumber:       prNumber,
			collapsedFiles: collapsedFiles,
		}

		// Convert comments to tree structure
		browseItems := buildCommentTree(comments)

		// Create resolve actions
		resolveAction := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", nil // Cannot resolve a file header
			}
			return resolveCommentAction(client, prNumber, item.Comment)
		}

		// Create open action (on 'o')
		openAction := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", nil // Cannot open a file header
			}
			// Use cached URL from initial fetch - no additional API calls
			if item.Comment.HTMLURL == "" {
				return "", fmt.Errorf("comment has no URL")
			}
			if err := openURLInBrowser(item.Comment.HTMLURL); err != nil {
				return "", err
			}
			return fmt.Sprintf("Opened comment %d in browser", item.Comment.ID), nil
		}

		// Filter function (hide resolved and collapsed)
		filterFunc := func(item BrowseItem, hideResolved bool) bool {
			return browseFilter(item, hideResolved, collapsedFiles)
		}

		// Handle selection (Enter key)
		onSelect := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				collapsedFiles[item.Path] = !collapsedFiles[item.Path]
				return "", nil // Just toggle collapse
			}

			// Return empty string to allow detail view to open
			return "", nil
		}

		// Editor actions for R (resolve with comment)
		editorPrepareR := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", fmt.Errorf("cannot add comment to file header")
			}
			if item.Comment.ThreadID == "" {
				return "", fmt.Errorf("comment has no thread ID")
			}
			return "", nil // No initial content for resolve+comment
		}

		editorCompleteR := func(item BrowseItem, body string) (string, error) {
			comment := item.Comment
			reply, err := client.ReplyToReviewComment(prNumber, comment.ID, body)
			if err != nil {
				return "", fmt.Errorf("failed to add comment: %w", err)
			}

			// Add reply to local thread so it shows in details view
			comment.ThreadComments = append(comment.ThreadComments, *reply)

			// Toggle resolved state
			statusMsg, err := resolveCommentAction(client, prNumber, comment)
			if err != nil {
				return "", err
			}

			if reply != nil && reply.HTMLURL != "" {
				link := ui.CreateHyperlink(reply.HTMLURL, "a comment")
				return fmt.Sprintf("%s\nPosted %s.", statusMsg, link), nil
			}

			return statusMsg, nil
		}

		// Editor actions for Q (quote reply without context)
		editorPrepareQ := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", fmt.Errorf("cannot quote reply to file header")
			}
			comment := item.Comment
			// Get author and body based on selected comment index
			author, body := comment.Author, comment.Body
			if item.SelectedCommentIdx > 0 && item.SelectedCommentIdx-1 < len(comment.ThreadComments) {
				tc := comment.ThreadComments[item.SelectedCommentIdx-1]
				author, body = tc.Author, tc.Body
			}
			return ui.FormatQuotedReply(
				author,
				body,
				comment.DiffHunk,
				comment.Path,
				false, // Don't include context
			), nil
		}

		editorCompleteQ := func(item BrowseItem, body string) (string, error) {
			comment := item.Comment
			reply, err := client.ReplyToReviewComment(prNumber, comment.ID, body)
			if err != nil {
				return "", fmt.Errorf("failed to post reply: %w", err)
			}

			// Add reply to local thread so it shows in details view
			comment.ThreadComments = append(comment.ThreadComments, *reply)

			url := reply.HTMLURL
			if url == "" {
				return fmt.Sprintf("Posted comment %d", reply.ID), nil
			}

			link := ui.CreateHyperlink(url, "a comment")
			return fmt.Sprintf("Posted %s.", link), nil
		}

		// Editor actions for C (quote reply with context)
		editorPrepareC := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", fmt.Errorf("cannot quote reply to file header")
			}
			comment := item.Comment
			// Get author and body based on selected comment index
			author, body := comment.Author, comment.Body
			if item.SelectedCommentIdx > 0 && item.SelectedCommentIdx-1 < len(comment.ThreadComments) {
				tc := comment.ThreadComments[item.SelectedCommentIdx-1]
				author, body = tc.Author, tc.Body
			}
			return ui.FormatQuotedReply(
				author,
				body,
				comment.DiffHunk,
				comment.Path,
				true, // Include context
			), nil
		}

		// editorCompleteC is the same as editorCompleteQ - just post the reply
		editorCompleteC := editorCompleteQ

		// Callback to check if an item is resolved (for dynamic help text)
		isItemResolved := func(item BrowseItem) bool {
			if item.Type == "file" {
				return false
			}
			return item.Comment.IsResolved()
		}

		// Callback to refresh items from the API
		refreshItems := func() ([]BrowseItem, error) {
			freshComments, err := client.FetchReviewComments(prNumber)
			if err != nil {
				return nil, err
			}
			return buildCommentTree(freshComments), nil
		}

		// Agent action - launch coding agent with comment details
		agentAction := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", fmt.Errorf("cannot launch agent on file header")
			}
			comment := item.Comment
			// Get body based on selected comment index
			body := comment.Body
			if item.SelectedCommentIdx > 0 && item.SelectedCommentIdx-1 < len(comment.ThreadComments) {
				body = comment.ThreadComments[item.SelectedCommentIdx-1].Body
			}
			prompt := fmt.Sprintf("Review comment on %s:%d\n\n%s",
				comment.Path,
				comment.Line,
				body)
			return "LAUNCH_AGENT:" + prompt, nil
		}

		// Edit action - open file in editor at comment line
		editAction := func(item BrowseItem) (string, error) {
			if item.Type == "file" {
				return "", fmt.Errorf("cannot edit file header")
			}
			return fmt.Sprintf("EDIT_FILE:%s:%d", item.Comment.Path, item.Comment.Line), nil
		}

		// Reaction action - get comment ID for reaction
		reactionAction := func(item BrowseItem) (int64, error) {
			if item.Type == "file" {
				return 0, fmt.Errorf("cannot react to file header")
			}
			comment := item.Comment
			// Get the right comment based on SelectedCommentIdx
			if item.SelectedCommentIdx > 0 && item.SelectedCommentIdx-1 < len(comment.ThreadComments) {
				return comment.ThreadComments[item.SelectedCommentIdx-1].ID, nil
			}
			return comment.ID, nil
		}

		// Reaction complete - apply the reaction via API and update cached data
		reactionComplete := func(item BrowseItem, commentID int64, apiName, displayEmoji string) (string, error) {
			err := client.AddReactionToComment(prNumber, commentID, apiName)
			if err != nil {
				return "", err
			}

			// Fetch updated reactions and update the cached comment
			updatedReactions, err := client.FetchCommentReactions(prNumber, commentID)
			if err == nil && updatedReactions != nil {
				// Update the correct comment based on SelectedCommentIdx
				if item.SelectedCommentIdx == 0 {
					// Update main comment
					item.Comment.Reactions = *updatedReactions
				} else if item.SelectedCommentIdx-1 < len(item.Comment.ThreadComments) {
					// Update thread reply
					item.Comment.ThreadComments[item.SelectedCommentIdx-1].Reactions = *updatedReactions
				}
			}

			repo, err := client.GetRepo()
			if err != nil {
				// The reaction was added, but we can't create the URL.
				// Return a success message without the URL.
				return fmt.Sprintf("%s reaction added.", displayEmoji), nil
			}
			url := fmt.Sprintf("https://github.com/%s/pull/%d#discussion_r%d", repo, prNumber, commentID)
			link := ui.CreateHyperlink(url, "reaction added")
			return fmt.Sprintf("%s %s.", displayEmoji, link), nil
		}

		// Apply suggestion actions
		app := applier.New()
		app.SetDebug(browseDebug)
		app.SetGitHubClient(client)
		renderer.applier = app

		checkSuggestionPreconditions := func(item BrowseItem) error {
			if item.Type == "file" {
				return fmt.Errorf("cannot apply to file header")
			}
			if !item.Comment.HasSuggestion {
				return fmt.Errorf("no suggestion to apply")
			}
			return nil
		}

		applyAndGetMessage := func(item BrowseItem) (string, error) {
			if err := checkSuggestionPreconditions(item); err != nil {
				return "", err
			}
			if err := app.ApplySuggestion(item.Comment); err != nil {
				return "", err
			}
			return fmt.Sprintf("Applied suggestion to %s:%d", item.Comment.Path, item.Comment.Line), nil
		}

		applySuggestionPreview := func(item BrowseItem) (string, error) {
			if err := checkSuggestionPreconditions(item); err != nil {
				return "", err
			}
			return app.PreviewSuggestion(item.Comment)
		}

		applySuggestionAction := func(item BrowseItem) (string, error) {
			return applyAndGetMessage(item)
		}

		applySuggestionResolveAction := func(item BrowseItem) (string, error) {
			msg, err := applyAndGetMessage(item)
			if err != nil {
				return "", err
			}
			// Resolve the thread if possible
			if item.Comment.ThreadID != "" && !item.Comment.IsResolved() {
				if err := client.ResolveThread(item.Comment.ThreadID); err != nil {
					return msg + " (failed to resolve thread)", nil
				}
				item.Comment.SubjectType = "resolved"
				msg += " and resolved thread"
			}
			return msg, nil
		}

		selected, err := ui.Select(ui.SelectorOptions[BrowseItem]{
			Items:    browseItems,
			Renderer: renderer,

			// Core callbacks
			OnSelect:       onSelect,
			OnOpen:         openAction,
			FilterFunc:     filterFunc,
			FilterDefault:  true, // Hide resolved comments by default
			IsItemResolved: isItemResolved,
			RefreshItems:   refreshItems,
			CountableItem:  browseCountable,

			// r/u key: resolve/unresolve
			ResolveAction: resolveAction,
			ResolveKey:    "r resolve",
			ResolveKeyAlt: "u unresolve",

			// R/U key: resolve+comment via editor
			ResolveCommentPrepare:  editorPrepareR,
			ResolveCommentComplete: editorCompleteR,
			ResolveCommentKey:      "R resolve+comment",
			ResolveCommentKeyAlt:   "U unresolve+comment",

			// Q key: quote reply via editor
			QuotePrepare:  editorPrepareQ,
			QuoteComplete: editorCompleteQ,
			QuoteKey:      "Q quote",

			// C key: quote+context via editor
			QuoteContextPrepare:  editorPrepareC,
			QuoteContextComplete: editorCompleteC,
			QuoteContextKey:      "C quote+context",

			// a key: launch coding agent
			AgentAction: agentAction,
			AgentKey:    "a agent",

			// e key: edit file
			EditAction: editAction,
			EditKey:    "e edit",

			// x key: add reaction
			ReactionAction:   reactionAction,
			ReactionComplete: reactionComplete,
			ReactionKey:      "x react",

			// s key: apply suggestion
			ApplySuggestionPreview: applySuggestionPreview,
			ApplySuggestionAction:  applySuggestionAction,
			ApplySuggestionKey:     "s apply",

			// S key: apply suggestion and resolve
			ApplySuggestionResolveAction: applySuggestionResolveAction,
			ApplySuggestionResolveKey:    "S apply+resolve",
		})
		if err != nil {
			if errors.Is(err, ui.ErrNoSelection) {
				return nil
			}
			return fmt.Errorf("selection cancelled: %w", err)
		}

		if selected.Type == "file" {
			// If they selected a header and quit (enter), maybe just do nothing or open the file?
			// For now, let's assume they meant to select a comment.
			// But since we return on Enter, we need to handle it.
			// Let's just print a message.
			fmt.Println("Selected a file header. Please select a comment.")
			return nil
		}

		commentID = selected.Comment.ID
	} else if len(args) == 1 {
		// One argument: treat as COMMENT_ID, infer PR from current branch
		commentID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid comment ID: %s", args[0])
		}
		prNumber, err = getPRNumberWithSelection([]string{}, client)
		if err != nil {
			return err
		}
	} else if len(args) == 2 {
		// Two arguments: first is PR, second is COMMENT_ID
		prNumber, err = strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[0])
		}
		commentID, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid comment ID: %s", args[1])
		}
	}

	// Open comment in browser
	return openCommentInBrowser(client, prNumber, commentID)
}

func openCommentInBrowser(client *github.Client, prNumber int, commentID int64) error {
	// Fetch review comments to find the comment URL
	// Note: This function is only used from CLI path where we don't have cached data
	comments, err := client.FetchReviewComments(prNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch review comments: %w", err)
	}

	// Find the comment with the given ID
	var commentURL string
	for _, comment := range comments {
		if comment.ID == commentID {
			commentURL = comment.HTMLURL
			break
		}
	}

	if commentURL == "" {
		return fmt.Errorf("comment ID %d not found in PR #%d", commentID, prNumber)
	}

	return openURLInBrowser(commentURL)
}

// openURLInBrowser opens the given URL in the system's default browser
func openURLInBrowser(url string) error {
	var openCmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		openCmd = exec.Command("open", url)
	case "linux":
		openCmd = exec.Command("xdg-open", url)
	case "windows":
		openCmd = exec.Command("cmd", "/c", "start", url)
	default:
		openCmd = exec.Command("xdg-open", url)
	}

	if err := openCmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	return nil
}

// BrowseItem represents an item in the browse list (either a file header or a comment)
type BrowseItem struct {
	Type               string // "file", "comment", "comment_preview"
	Path               string
	Comment            *github.ReviewComment
	IsPreview          bool
	SelectedCommentIdx int  // 0 = main comment, 1+ = thread reply index
	HasUnresolved      bool // for "file" headers: whether the file has any unresolved comment
}

// coderabbitSummary extracts the severity and the title from a CodeRabbit
// review comment body, reporting false for a body that is not one.
func coderabbitSummary(body string) (severity, title string, ok bool) {
	lines := strings.Split(body, "\n")
	header := coderabbitSeverityRe.FindStringSubmatch(lines[0])
	if header == nil {
		return "", "", false
	}

	// CodeRabbit security findings open with taxonomy labels such as
	// "Sensitive Data Exposure (CWE-200):" and "Reachability:" ahead of the
	// sentence that names the finding. Prefer that sentence, and settle for
	// the first label only when nothing else follows it.
	var label string
	for _, line := range lines[1:] {
		match := coderabbitTitleRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		candidate := strings.TrimSpace(match[1])
		if !strings.HasSuffix(candidate, ":") {
			return strings.TrimSpace(header[1]), candidate, true
		}
		if label == "" {
			label = candidate
		}
	}
	if label != "" {
		return strings.TrimSpace(header[1]), label, true
	}
	return "", "", false
}

// coderabbitExcerpt renders the tree summary for a CodeRabbit review comment,
// returning an empty string for a body that is not one.
func coderabbitExcerpt(body string) string {
	severity, title, ok := coderabbitSummary(body)
	if !ok {
		return ""
	}
	return ui.EmojiText(severity, severityLeadInRe.ReplaceAllString(severity, "")) + ": " + title
}

// firstLineExcerpt returns the first line of a comment body, marked with an
// ellipsis when the body carries more lines.
func firstLineExcerpt(body string) string {
	lines := strings.Split(ui.StripSuggestionBlock(body), "\n")
	preview := lines[0]
	if len(lines) > 1 {
		preview += "..."
	}
	return preview
}

// browseFilter reports whether item should be visible, given whether resolved
// comments are hidden and which files are collapsed.
func browseFilter(item BrowseItem, hideResolved bool, collapsedFiles map[string]bool) bool {
	if item.Type == "file" {
		// A header for a file with nothing left to show would stand alone
		// above an empty section.
		if hideResolved {
			return item.HasUnresolved
		}
		return true
	}

	if collapsedFiles[item.Path] {
		return false
	}

	if hideResolved {
		return !item.Comment.IsResolved()
	}

	return true
}

// browseCountable reports whether item is a review comment rather than a row
// that exists only for layout, such as a file header or a preview row.
func browseCountable(item BrowseItem) bool {
	return item.Type == "comment"
}

// buildCommentTree converts a flat list of comments into a tree-like structure
func buildCommentTree(comments []*github.ReviewComment) []BrowseItem {
	// Sort comments by Path then Line
	// We need a stable sort for the tree structure
	// Make a copy to avoid modifying original slice if needed
	sortedComments := make([]*github.ReviewComment, len(comments))
	copy(sortedComments, comments)

	// Simple bubble sort or similar isn't needed, just use standard sort with custom comparator
	// But we need to import sort. Let's do it manually or add import.
	// Since I can't easily add imports without context, I'll assume sort is available or use a simple swap.
	// Actually, let's just use a simple grouping logic.

	// Group by file
	files := make(map[string][]*github.ReviewComment)
	var filePaths []string

	for _, c := range comments {
		if _, exists := files[c.Path]; !exists {
			filePaths = append(filePaths, c.Path)
		}
		files[c.Path] = append(files[c.Path], c)
	}

	// Sort file paths
	// We need to sort strings. I'll implement a simple string sort since I can't see imports easily.
	for i := 0; i < len(filePaths); i++ {
		for j := i + 1; j < len(filePaths); j++ {
			if filePaths[i] > filePaths[j] {
				filePaths[i], filePaths[j] = filePaths[j], filePaths[i]
			}
		}
	}

	var items []BrowseItem

	for _, path := range filePaths {
		fileComments := files[path]

		hasUnresolved := false
		for _, c := range fileComments {
			if !c.IsResolved() {
				hasUnresolved = true
				break
			}
		}

		// Add File Header
		items = append(items, BrowseItem{
			Type:          "file",
			Path:          path,
			HasUnresolved: hasUnresolved,
		})

		// Sort comments in this file by line
		for i := 0; i < len(fileComments); i++ {
			for j := i + 1; j < len(fileComments); j++ {
				if fileComments[i].Line > fileComments[j].Line {
					fileComments[i], fileComments[j] = fileComments[j], fileComments[i]
				}
			}
		}

		// Add Comments
		for _, c := range fileComments {
			// Main comment item
			items = append(items, BrowseItem{
				Type:    "comment",
				Path:    path,
				Comment: c,
			})
			// Preview item (skippable)
			items = append(items, BrowseItem{
				Type:      "comment_preview",
				Path:      path,
				Comment:   c,
				IsPreview: true,
			})
		}
	}

	return items
}

// browseItemRenderer implements ui.ItemRenderer for BrowseItem
type browseItemRenderer struct {
	repo           string
	prNumber       int
	collapsedFiles map[string]bool
	applier        *applier.Applier
}

func (r *browseItemRenderer) Title(item BrowseItem) string {
	if item.Type == "file" {
		icon := "▼"
		collapsedIcon := "▶"
		folder := "📂"
		if !ui.ColorsEnabled() {
			icon = "-"
			collapsedIcon = "+"
			folder = ""
		}
		if r.collapsedFiles != nil && r.collapsedFiles[item.Path] {
			icon = collapsedIcon
		}
		title := fmt.Sprintf("%s %s", icon, item.Path)
		if folder != "" {
			title = fmt.Sprintf("%s %s %s", icon, folder, item.Path)
		}
		return ui.Colorize(ui.ColorCyan, strings.TrimSpace(title))
	}

	if item.IsPreview {
		// Show truncated body for preview item in gray
		// Note: This works because IsSkippable returns false, so lipgloss
		// won't re-style this text and interfere with the ANSI codes
		preview := coderabbitExcerpt(item.Comment.Body)
		if preview == "" {
			preview = firstLineExcerpt(item.Comment.Body)
		}
		// Truncate on visible columns so that a multi-byte rune is never
		// sliced in half.
		if ansi.StringWidth(preview) > 80 {
			preview = ansi.Truncate(preview, 80, "...")
		}
		// The indent stays outside the link so that only the excerpt itself
		// is clickable.
		excerpt := ui.CreateHyperlink(item.Comment.HTMLURL, ui.Colorize(ui.ColorGray, preview))
		return "      " + excerpt
	}

	// Comment Metadata
	style := ui.NewReviewListStyle(item.Comment.Author, item.Comment.IsResolved())
	// Indent with tree structure
	title := fmt.Sprintf("  └── %s Line %d", style.FormatCommentTitle(item.Comment.ID), item.Comment.Line)
	// Add reply count if there are replies
	if len(item.Comment.ThreadComments) > 0 {
		replyCount := len(item.Comment.ThreadComments)
		replyText := "reply"
		if replyCount > 1 {
			replyText = "replies"
		}
		title += fmt.Sprintf(" [%d %s]", replyCount, replyText)
	}
	title += " " + style.Status.Format(true)
	return title
}

func (r *browseItemRenderer) Description(item BrowseItem) string {
	return ""
}

func (r *browseItemRenderer) Preview(item BrowseItem) string {
	return r.PreviewWithHighlight(item, -1) // No highlight
}

func (r *browseItemRenderer) PreviewWithHighlight(item BrowseItem, highlightIdx int) string {
	if item.Type == "file" {
		return fmt.Sprintf("File: %s\n\nSelect a comment below to view details.", item.Path)
	}

	// Reuse the logic from browseCommentRenderer but adapted for BrowseItem
	comment := item.Comment
	var preview strings.Builder

	// Header
	status := "unresolved"
	statusColor := ui.ColorYellow
	if comment.IsResolved() {
		status = "resolved"
		statusColor = ui.ColorGreen
	}
	preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("Author: @%s\n", comment.Author)))
	preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("Location: %s:%d\n", comment.Path, comment.Line)))
	preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("Status: %s\n", ui.Colorize(statusColor, status))))
	if comment.HTMLURL != "" {
		preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("URL: %s\n", ui.CreateHyperlink(comment.HTMLURL, comment.HTMLURL))))
	}
	if !comment.CreatedAt.IsZero() {
		preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("Time: %s\n", ui.FormatRelativeTime(comment.CreatedAt))))
	}

	// Display reactions if any
	reactions := ui.FormatReactions(ui.ReactionCountsFromGitHub(comment.Reactions))
	if reactions != "" {
		preview.WriteString(ui.Colorize(ui.ColorCyan, fmt.Sprintf("Reactions: %s\n", reactions)))
	}

	if comment.IsOutdated {
		preview.WriteString(ui.Colorize(ui.ColorYellow, ui.EmojiText("⚠️  OUTDATED\n", "OUTDATED\n")))
	}

	// Comment body (with markdown rendering, truncated to first 200 lines of source)
	body := ui.StripSuggestionBlock(comment.Body)
	if body != "" {
		// Highlight indicator for main comment (idx 0)
		if highlightIdx == 0 {
			preview.WriteString(ui.Colorize(ui.ColorMagenta, "\n▶▶▶ SELECTED COMMENT ◀◀◀\n"))
		}
		preview.WriteString("\n--- Comment ---\n")

		// Truncate very long comments before rendering to avoid slowness
		bodyLines := strings.Split(body, "\n")
		if len(bodyLines) > 200 {
			body = strings.Join(bodyLines[:200], "\n") + "\n\n...(truncated, content too long)"
		}

		// Try to render markdown
		rendered, err := ui.RenderMarkdown(body)
		if err == nil && rendered != "" {
			preview.WriteString(rendered)
		} else {
			// Fallback to wrapped text
			preview.WriteString(ui.WrapText(body, 80))
		}
		preview.WriteString("\n")
		if highlightIdx == 0 {
			preview.WriteString(ui.Colorize(ui.ColorMagenta, "▶▶▶ END SELECTED ◀◀◀\n"))
		}
	}

	// Suggestion diff (show actual diff if applier is available and file exists locally)
	if comment.HasSuggestion && comment.SuggestedCode != "" {
		var showedDiff bool
		if r.applier != nil {
			if diffStr, err := r.applier.PreviewSuggestion(comment); err == nil {
				preview.WriteString(ui.Colorize(ui.ColorCyan, "\n--- Suggestion Diff ---\n"))
				preview.WriteString(ui.ColorizeDiff(diffStr))
				preview.WriteString("\n")
				showedDiff = true
			}
		}
		if !showedDiff {
			preview.WriteString(ui.Colorize(ui.ColorCyan, "\n--- Suggested Code ---\n"))
			lang := ui.CodeFenceLanguageFromPath(comment.Path)
			md := fmt.Sprintf("```%s\n%s\n```", lang, comment.SuggestedCode)
			if rendered, err := ui.RenderMarkdown(md); err == nil && rendered != "" {
				preview.WriteString(rendered)
			} else {
				preview.WriteString(ui.Colorize(ui.ColorGreen, comment.SuggestedCode))
			}
			preview.WriteString("\n")
		}
	}

	// Diff hunk/context (show the last lines closest to the comment)
	if comment.DiffHunk != "" {
		diffLines := strings.Split(comment.DiffHunk, "\n")
		if len(diffLines) > 2 {
			preview.WriteString(ui.Colorize(ui.ColorCyan, "\n--- Context ---\n"))
			truncated := ui.TruncateDiffTail(comment.DiffHunk, 8)
			preview.WriteString(ui.ColorizeDiff(truncated))
			preview.WriteString("\n")
		}
	}

	// Thread replies (with markdown rendering, truncated to first 100 lines each)
	if len(comment.ThreadComments) > 0 {
		preview.WriteString("\n--- Replies ---\n")
		for i, threadComment := range comment.ThreadComments {
			// Add vertical spacing before each reply
			preview.WriteString("\n")

			// Highlight indicator for this reply (idx = i+1)
			isHighlighted := highlightIdx == i+1
			if isHighlighted {
				preview.WriteString(ui.Colorize(ui.ColorMagenta, "▶▶▶ SELECTED REPLY ◀◀◀\n"))
			}

			// Format: Reply N by @author | URL | time ago
			replyHeader := fmt.Sprintf("Reply %d by @%s", i+1, threadComment.Author)
			if threadComment.HTMLURL != "" {
				replyHeader += fmt.Sprintf(" | %s", ui.CreateHyperlink(threadComment.HTMLURL, threadComment.HTMLURL))
			}
			if !threadComment.CreatedAt.IsZero() {
				replyHeader += fmt.Sprintf(" | %s", ui.FormatRelativeTime(threadComment.CreatedAt))
			}
			preview.WriteString(replyHeader + "\n")

			// Display reactions for thread comment if any
			replyReactions := ui.FormatReactions(ui.ReactionCountsFromGitHub(threadComment.Reactions))
			if replyReactions != "" {
				fmt.Fprintf(&preview, "Reactions: %s\n", replyReactions)
			}

			// Truncate very long replies before rendering to avoid slowness
			replyBody := threadComment.Body
			replyLines := strings.Split(replyBody, "\n")
			if len(replyLines) > 100 {
				replyBody = strings.Join(replyLines[:100], "\n") + "\n\n...(truncated, content too long)"
			}

			// Render reply body with markdown
			rendered, err := ui.RenderMarkdown(replyBody)
			if err == nil && rendered != "" {
				preview.WriteString(rendered)
			} else {
				preview.WriteString(ui.WrapText(replyBody, 80))
			}
			preview.WriteString("\n")

			if isHighlighted {
				preview.WriteString(ui.Colorize(ui.ColorMagenta, "▶▶▶ END SELECTED ◀◀◀\n"))
			}
		}
	}

	return preview.String()
}

func (r *browseItemRenderer) EditPath(item BrowseItem) string {
	return item.Path
}

func (r *browseItemRenderer) EditLine(item BrowseItem) int {
	if item.Type == "file" {
		return 0
	}
	return item.Comment.Line
}

func (r *browseItemRenderer) FilterValue(item BrowseItem) string {
	if item.Type == "file" {
		return item.Path
	}
	return item.Path + " " + r.Title(item) + " " + r.Description(item) + " " + item.Comment.Body
}

func (r *browseItemRenderer) IsSkippable(item BrowseItem) bool {
	// Preview items are supplementary info, not "skippable" in the sense of
	// being invalid/crossed-out. Return false to avoid strikethrough styling.
	return false
}

func (r *browseItemRenderer) ThreadCommentCount(item BrowseItem) int {
	if item.Type == "file" || item.Comment == nil {
		return 0
	}
	return 1 + len(item.Comment.ThreadComments) // main + replies
}

func (r *browseItemRenderer) ThreadCommentPreview(item BrowseItem, idx int) string {
	if item.Comment == nil {
		return ""
	}
	var author, body string
	if idx == 0 {
		author = item.Comment.Author
		body = item.Comment.Body
	} else if idx-1 < len(item.Comment.ThreadComments) {
		tc := item.Comment.ThreadComments[idx-1]
		author = tc.Author
		body = tc.Body
	}

	// Strip markdown images ![alt](url) and convert links [text](url) to just text
	body = stripMarkdownForPreview(body)

	// Skip quoted lines (starting with ">") to show the actual comment content
	var nonQuotedLines []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, ">") {
			nonQuotedLines = append(nonQuotedLines, trimmed)
		}
	}
	body = strings.Join(nonQuotedLines, " ")
	// Truncate to ~100 chars
	if len(body) > 100 {
		body = body[:97] + "..."
	}
	return fmt.Sprintf("@%s: %s", author, body)
}

func (r *browseItemRenderer) WithSelectedComment(item BrowseItem, idx int) BrowseItem {
	item.SelectedCommentIdx = idx
	return item
}

// stripMarkdownForPreview removes images and converts links to plain text
func stripMarkdownForPreview(text string) string {
	// Remove markdown images ![alt](url)
	text = markdownImageRe.ReplaceAllString(text, "")

	// Convert markdown links [text](url) to just text
	text = markdownLinkRe.ReplaceAllString(text, "$1")

	return strings.TrimSpace(text)
}

// resolveCommentAction resolves a review comment thread
func resolveCommentAction(client *github.Client, prNumber int, comment *github.ReviewComment) (string, error) {
	if comment.ThreadID == "" {
		return "", fmt.Errorf("comment has no thread ID")
	}

	if comment.IsResolved() {
		// Unresolve
		if err := client.UnresolveThread(comment.ThreadID); err != nil {
			return "", err
		}
		comment.SubjectType = "line" // Reset to default
		return "Marked as unresolved", nil
	} else {
		// Resolve
		if err := client.ResolveThread(comment.ThreadID); err != nil {
			return "", err
		}
		comment.SubjectType = "resolved"
		return "Marked as resolved", nil
	}
}
