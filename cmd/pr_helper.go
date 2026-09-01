package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
)

// getPRNumberWithSelection attempts to get PR number from args, current branch,
// or interactive selection. Falls back to interactive PR selector if current
// branch has no associated PR.
func getPRNumberWithSelection(args []string, client *github.Client) (int, error) {
	// Try explicit PR number from args first
	if len(args) > 0 {
		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, fmt.Errorf("invalid PR number: %s", args[0])
		}
		return prNumber, nil
	}

	// Try to get PR from current branch
	prNumber, err := client.GetCurrentBranchPR()
	if err == nil {
		fmt.Fprintf(os.Stderr, "Auto-detected PR #%d for current branch\n", prNumber)
		return prNumber, nil
	}

	// Fallback: Interactive PR selection
	prs, err := client.ListOpenPRs()
	if err != nil {
		return 0, fmt.Errorf("no PR found for current branch and failed to list PRs: %w", err)
	}

	if len(prs) == 0 {
		return 0, fmt.Errorf("no open pull requests found")
	}

	selected, err := ui.SelectPR(prs)
	if err != nil {
		if errors.Is(err, ui.ErrNoSelection) {
			os.Exit(0) // Silent exit on cancel
		}
		return 0, err
	}

	return selected.Number, nil
}

// getRepoFromClient extracts the repository name from the client
func getRepoFromClient(client *github.Client) string {
	// Use the global repoFlag if set
	if repoFlag != "" {
		return repoFlag
	}

	// Try to get repo from the client's internal method
	repo, err := client.GetRepo()
	if err == nil && repo != "" {
		return repo
	}

	// Fallback - we'll construct the URL without the repo part
	return "owner/repo"
}
