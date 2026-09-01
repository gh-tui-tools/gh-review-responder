package ui

import (
	"fmt"
	"strings"

	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
)

// prItemRenderer implements ItemRenderer for PullRequest
type prItemRenderer struct{}

func (r *prItemRenderer) Title(pr *github.PullRequest) string {
	// Format: "#123 Fix authentication bug"
	title := fmt.Sprintf("#%d %s", pr.Number, pr.Title)
	return Colorize(ColorCyan, title)
}

func (r *prItemRenderer) Description(pr *github.PullRequest) string {
	// Format: "by @author • ✓ Approved • Draft"
	// This line is skippable during navigation
	parts := []string{fmt.Sprintf("by @%s", pr.Author)}

	if pr.ReviewDecision != "" {
		parts = append(parts, formatReviewStatus(pr.ReviewDecision))
	}

	if pr.IsDraft {
		parts = append(parts, Colorize(ColorGray, "[Draft]"))
	}

	description := strings.Join(parts, " • ")
	return "  " + Colorize(ColorGray, description)
}

func (r *prItemRenderer) Preview(pr *github.PullRequest) string {
	return r.PreviewWithHighlight(pr, -1)
}

func (r *prItemRenderer) PreviewWithHighlight(pr *github.PullRequest, highlightIdx int) string {
	var preview strings.Builder

	// Header
	preview.WriteString(Colorize(ColorCyan, fmt.Sprintf("Pull Request #%d\n", pr.Number)))
	preview.WriteString(Colorize(ColorCyan, strings.Repeat("=", 50)))
	preview.WriteString("\n\n")

	fmt.Fprintf(&preview, "Title: %s\n", pr.Title)
	fmt.Fprintf(&preview, "Author: @%s\n", pr.Author)
	fmt.Fprintf(&preview, "Branch: %s\n", pr.HeadRefName)

	if pr.IsDraft {
		preview.WriteString(Colorize(ColorYellow, "\nStatus: Draft\n"))
	}

	if pr.ReviewDecision != "" {
		fmt.Fprintf(&preview, "\nReview Status: %s\n", formatReviewStatus(pr.ReviewDecision))
	}

	preview.WriteString("\n" + Colorize(ColorGray, "Press Enter to select this PR"))

	return preview.String()
}

func (r *prItemRenderer) FilterValue(pr *github.PullRequest) string {
	// Allow filtering by number, title, or author
	return fmt.Sprintf("%d %s %s", pr.Number, pr.Title, pr.Author)
}

func (r *prItemRenderer) EditPath(pr *github.PullRequest) string {
	return "" // Not applicable for PRs
}

func (r *prItemRenderer) EditLine(pr *github.PullRequest) int {
	return 0 // Not applicable for PRs
}

func (r *prItemRenderer) IsSkippable(pr *github.PullRequest) bool {
	return false // No skippable items in PR list (description is part of Title rendering)
}

func (r *prItemRenderer) ThreadCommentCount(pr *github.PullRequest) int {
	return 0 // Not applicable for PRs
}

func (r *prItemRenderer) ThreadCommentPreview(pr *github.PullRequest, idx int) string {
	return "" // Not applicable for PRs
}

func (r *prItemRenderer) WithSelectedComment(pr *github.PullRequest, idx int) *github.PullRequest {
	return pr // No-op for PRs
}

// formatReviewStatus formats the review decision with appropriate color and emoji
func formatReviewStatus(decision string) string {
	switch decision {
	case "APPROVED":
		return Colorize(ColorGreen, EmojiText("✓ Approved", "Approved"))
	case "CHANGES_REQUESTED":
		return Colorize(ColorRed, EmojiText("✗ Changes requested", "Changes requested"))
	case "REVIEW_REQUIRED":
		return Colorize(ColorYellow, EmojiText("○ Review required", "Review required"))
	default:
		return decision
	}
}

// SelectPR displays an interactive selector for choosing a pull request
func SelectPR(prs []*github.PullRequest) (*github.PullRequest, error) {
	renderer := &prItemRenderer{}
	return SelectFromList(prs, renderer)
}
