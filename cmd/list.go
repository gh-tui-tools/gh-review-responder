package cmd

import (
	"fmt"
	"strings"

	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
	"github.com/spf13/cobra"
)

var (
	listShowResolved bool
	listDebug        bool
	listLLM          bool
	listJSON         bool
	listCodeContext  bool
)

var listCmd = &cobra.Command{
	Use:   "list [PR_NUMBER] [THREAD_ID]",
	Short: "List review comments for a pull request",
	Long:  `List all review comments and suggestions for a pull request.`,
	Args:  cobra.RangeArgs(0, 2),
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listShowResolved, "all", false, "Show resolved/done suggestions")
	listCmd.Flags().BoolVar(&listDebug, "debug", false, "Enable debug output")
	listCmd.Flags().BoolVar(&listLLM, "llm", false, "Output in a format suitable for LLM consumption")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output raw review comment JSON (includes thread replies)")
	listCmd.Flags().BoolVar(&listCodeContext, "code-context", false, "Display surrounding diff context for each comment")
}

func runList(cmd *cobra.Command, args []string) error {
	client := github.NewClient()
	client.SetDebug(listDebug)
	if repoFlag != "" {
		client.SetRepo(repoFlag)
	}

	if listJSON && listLLM {
		return fmt.Errorf("--json cannot be combined with --llm")
	}

	prNumber, err := getPRNumberWithSelection(args, client)
	if err != nil {
		return err
	}

	var threadID string
	if len(args) > 1 {
		threadID = args[1]
	}

	comments, err := client.FetchReviewComments(prNumber)
	if err != nil {
		return fmt.Errorf("failed to fetch review comments: %w", err)
	}

	// Filter out resolved comments unless --all is specified
	filteredComments := make([]*github.ReviewComment, 0)
	for _, comment := range comments {
		if listShowResolved || !comment.IsResolved() {
			filteredComments = append(filteredComments, comment)
		}
	}

	if threadID != "" {
		filteredComments = filterByThreadID(filteredComments, threadID)
	}

	if listJSON {
		if len(filteredComments) == 0 {
			if threadID != "" {
				return fmt.Errorf("no review comments found for thread ID %s", threadID)
			}
			fmt.Println("[]")
			return nil
		}

		jsonOutput, err := dumpCommentsJSON(client, prNumber, filteredComments)
		if err != nil {
			return err
		}
		fmt.Println(jsonOutput)
		return nil
	}

	if len(filteredComments) == 0 {
		if threadID != "" {
			fmt.Printf("No review comments found for thread ID %s.\n", threadID)
			return nil
		}
		if listShowResolved {
			fmt.Println("No review comments found.")
		} else {
			fmt.Println("No unresolved review comments found. Use --all to show resolved comments.")
		}
		return nil
	}

	// Use readable format if requested
	if listLLM {
		displayLLMFormat(filteredComments)
		return nil
	}

	fmt.Printf("Found %d review comment(s):\n", len(filteredComments))

	for i, comment := range filteredComments {
		displayComment(i+1, len(filteredComments), comment)
	}

	return nil
}

func filterByThreadID(comments []*github.ReviewComment, threadID string) []*github.ReviewComment {
	filtered := comments[:0]
	for _, comment := range comments {
		// Match by GraphQL thread ID or by comment database ID
		if comment.ThreadID == threadID || fmt.Sprintf("%d", comment.ID) == threadID {
			filtered = append(filtered, comment)
		}
	}
	return filtered
}

func dumpCommentsJSON(client *github.Client, prNumber int, comments []*github.ReviewComment) (string, error) {
	commentIDs := collectCommentIDs(comments)
	return client.DumpCommentsJSON(prNumber, commentIDs)
}

func collectCommentIDs(comments []*github.ReviewComment) []int64 {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0)

	addID := func(id int64) {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}

	for _, comment := range comments {
		addID(comment.ID)
		for _, reply := range comment.ThreadComments {
			addID(reply.ID)
		}
	}
	return ids
}

// displayComment displays a single review comment with formatting
func displayComment(index, total int, comment *github.ReviewComment) {
	// Create clickable link to the review comment
	fileLocation := fmt.Sprintf("%s:%d", comment.Path, comment.Line)
	clickableLocation := ui.CreateHyperlink(comment.HTMLURL, fileLocation)

	// Header
	fmt.Printf("\n%s\n",
		ui.Colorize(ui.ColorCyan, fmt.Sprintf("[%d/%d] %s by @%s (ID %d)",
			index, total, clickableLocation, comment.Author, comment.ID)))
	fmt.Printf("%s\n", ui.Colorize(ui.ColorGray, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))

	// Show resolved status
	if comment.IsResolved() {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorGreen, ui.EmojiText("✅ Resolved", "Resolved")))
	}

	// Show reactions if any
	reactions := ui.FormatReactions(ui.ReactionCountsFromGitHub(comment.Reactions))
	if reactions != "" {
		fmt.Printf("\n%s %s\n", ui.Colorize(ui.ColorYellow, "Reactions:"), reactions)
	}

	// Show the review comment (without the suggestion block)
	commentText := ui.StripSuggestionBlock(comment.Body)
	if commentText != "" {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorYellow, "Review comment:"))
		rendered, err := ui.RenderMarkdown(commentText)
		if err == nil && rendered != "" {
			fmt.Println(rendered)
		} else {
			// Fallback to wrapped text
			wrappedComment := ui.WrapText(commentText, 80)
			fmt.Printf("%s\n", wrappedComment)
		}
	}

	// Show the suggestion if present
	if comment.HasSuggestion {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorYellow, "Suggested change:"))
		fmt.Println(ui.ColorizeCode(comment.SuggestedCode))
	}

	// Show context (diff hunk) if available and requested
	if listCodeContext && comment.DiffHunk != "" {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorYellow, "Context:"))
		fmt.Println(ui.ColorizeDiff(comment.DiffHunk))
	}

	// Show thread comments (replies)
	if len(comment.ThreadComments) > 0 {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Thread replies:"))
		for i, threadComment := range comment.ThreadComments {
			fmt.Printf("\n  %s\n", ui.Colorize(ui.ColorGray, fmt.Sprintf("└─ Reply %d by @%s:", i+1, threadComment.Author)))

			// Show reactions for reply if any
			replyReactions := ui.FormatReactions(ui.ReactionCountsFromGitHub(threadComment.Reactions))
			if replyReactions != "" {
				fmt.Printf("     %s %s\n", ui.Colorize(ui.ColorYellow, "Reactions:"), replyReactions)
			}

			rendered, err := ui.RenderMarkdown(threadComment.Body)
			if err == nil && rendered != "" {
				// Indent the rendered markdown
				lines := strings.Split(rendered, "\n")
				for _, line := range lines {
					fmt.Printf("     %s\n", line)
				}
			} else {
				// Fallback to wrapped text
				wrappedReply := ui.WrapText(threadComment.Body, 75)
				lines := strings.Split(wrappedReply, "\n")
				for _, line := range lines {
					fmt.Printf("     %s\n", line)
				}
			}
		}
	}

	fmt.Println()
}

// displayLLMFormat displays review comments in a readable format for LLM consumption
func displayLLMFormat(comments []*github.ReviewComment) {
	for i, comment := range comments {
		if i > 0 {
			fmt.Println("---")
		}

		fmt.Printf("FILE: %s:%d\n", comment.Path, comment.Line)
		fmt.Printf("COMMENT_ID: %d\n", comment.ID)
		fmt.Printf("AUTHOR: %s\n", comment.Author)
		fmt.Printf("URL: %s\n", comment.HTMLURL)

		if comment.IsResolved() {
			fmt.Println("STATUS: resolved")
		} else {
			fmt.Println("STATUS: unresolved")
		}

		// Show the review comment (without suggestion block)
		commentText := ui.StripSuggestionBlock(comment.Body)
		if commentText != "" {
			fmt.Printf("COMMENT:\n%s\n", commentText)
		}

		// Show the suggestion if present
		if comment.HasSuggestion {
			fmt.Printf("SUGGESTION:\n%s\n", comment.SuggestedCode)
		}

		// Show thread replies
		if len(comment.ThreadComments) > 0 {
			fmt.Println("REPLIES:")
			for j, reply := range comment.ThreadComments {
				fmt.Printf("  [%d] %s: %s\n", j+1, reply.Author, reply.Body)
			}
		}
	}
}
