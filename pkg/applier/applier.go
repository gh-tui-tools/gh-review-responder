package applier

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ai"
	"github.com/gh-tui-tools/gh-review-responder/pkg/diffhunk"
	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
)

// errEditApplied is a sentinel error indicating that a patch was successfully applied via the edit flow
var errEditApplied = fmt.Errorf("patch applied after editing")

type Applier struct {
	debug        bool
	aiProvider   ai.AIProvider
	githubClient *github.Client
}

func New() *Applier {
	return &Applier{}
}

// SetDebug enables or disables debug output
func (a *Applier) SetDebug(debug bool) {
	a.debug = debug
}

// SetAIProvider configures the AI provider for intelligent application
func (a *Applier) SetAIProvider(provider ai.AIProvider) {
	a.aiProvider = provider
}

// SetGitHubClient sets the GitHub client for resolving threads
func (a *Applier) SetGitHubClient(client *github.Client) {
	a.githubClient = client
}

// ApplySuggestion applies a suggestion using the comment's StartLine/Line fields directly.
// This is used by the Browse view where the suggestion targets are known line ranges.
// The "apply" subcommand uses applySuggestion instead, which parses the diff hunk.
func (a *Applier) ApplySuggestion(comment *github.ReviewComment) error {
	if err := validatePath(comment.Path); err != nil {
		return err
	}

	fileContent, err := os.ReadFile(comment.Path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", comment.Path, err)
	}
	fileLines := strings.Split(string(fileContent), "\n")

	targetLine, removeCount, err := a.findReplacementTargetByLineRange(comment, fileLines)
	if err != nil {
		return err
	}

	var suggestionLines []string
	if comment.SuggestedCode != "" {
		suggestionLines = strings.Split(strings.TrimSuffix(comment.SuggestedCode, "\n"), "\n")
	}

	var newFileLines []string
	newFileLines = append(newFileLines, fileLines[:targetLine]...)
	newFileLines = append(newFileLines, suggestionLines...)
	if targetLine+removeCount < len(fileLines) {
		newFileLines = append(newFileLines, fileLines[targetLine+removeCount:]...)
	}

	newContent := strings.Join(newFileLines, "\n")
	if strings.HasSuffix(string(fileContent), "\n") && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}

	if err := os.WriteFile(comment.Path, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", comment.Path, err)
	}

	return nil
}

// PreviewSuggestion generates a diff preview of what applying the suggestion would change.
// Returns the diff string without actually modifying the file.
func (a *Applier) PreviewSuggestion(comment *github.ReviewComment) (string, error) {
	if err := validatePath(comment.Path); err != nil {
		return "", err
	}

	// Read the current file
	fileContent, err := os.ReadFile(comment.Path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", comment.Path, err)
	}
	fileLines := strings.Split(string(fileContent), "\n")

	// Find the lines to replace
	targetLine, removeCount, err := a.findReplacementTargetByLineRange(comment, fileLines)
	if err != nil {
		return "", err
	}

	// Build a unified diff preview
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- a/%s\n", comment.Path)
	fmt.Fprintf(&diff, "+++ b/%s\n", comment.Path)
	var suggestionLines []string
	if comment.SuggestedCode != "" {
		suggestionLines = strings.Split(strings.TrimSuffix(comment.SuggestedCode, "\n"), "\n")
	}
	fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n",
		targetLine+1, removeCount,
		targetLine+1, len(suggestionLines))

	// Show lines being removed
	for i := 0; i < removeCount; i++ {
		if targetLine+i < len(fileLines) {
			fmt.Fprintf(&diff, "-%s\n", fileLines[targetLine+i])
		}
	}

	// Show lines being added
	for _, line := range suggestionLines {
		fmt.Fprintf(&diff, "+%s\n", line)
	}

	return diff.String(), nil
}

// debugLog prints debug messages if debug mode is enabled
func (a *Applier) debugLog(format string, args ...interface{}) {
	if a.debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}

// validatePath checks that the given path does not escape the current working directory.
// This prevents path traversal attacks where a malicious PR could reference paths
// like "../../../etc/passwd" to read or write arbitrary files on the system.
func validatePath(path string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Resolve to absolute and clean the path (collapses ../ sequences)
	absPath := filepath.Clean(filepath.Join(cwd, path))
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	}

	// The resolved path must be within the working directory
	if !strings.HasPrefix(absPath, cwd+string(filepath.Separator)) {
		return fmt.Errorf("path %q resolves outside the repository (%s)", path, cwd)
	}

	return nil
}

// ApplyAll applies all suggestions without prompting
func (a *Applier) ApplyAll(suggestions []*github.ReviewComment) error {
	applied := 0
	failed := 0

	for _, suggestion := range suggestions {
		if err := a.applySuggestion(suggestion); err != nil {
			fmt.Printf("%sFailed to apply suggestion for %s:%d: %v\n",
				ui.EmojiText("❌ ", ""), suggestion.Path, suggestion.Line, err)
			failed++
		} else {
			fmt.Printf("%sApplied suggestion to %s:%d\n",
				ui.EmojiText("✅ ", ""), suggestion.Path, suggestion.Line)
			applied++

			// Show git diff of what was applied
			a.showGitDiff(suggestion.Path)
		}
	}

	fmt.Printf("\nApplied %d/%d suggestions (%d failed)\n", applied, len(suggestions), failed)
	return nil
}

// ApplyInteractive prompts the user for each suggestion using an interactive selector
func (a *Applier) ApplyInteractive(suggestions []*github.ReviewComment) error {
	applied := 0
	skipped := 0
	remaining := make([]*github.ReviewComment, len(suggestions))
	copy(remaining, suggestions)

	for len(remaining) > 0 {
		// Use interactive selector to choose next suggestion
		renderer := &suggestionRenderer{aiAvailable: a.aiProvider != nil}
		selected, err := ui.SelectFromList(remaining, renderer)
		if err != nil {
			fmt.Printf("\n%s\n", ui.Colorize(ui.ColorGray, "Selection cancelled"))
			break
		}

		// Show detailed view of selected suggestion
		a.showSuggestionDetails(selected, applied+skipped+1, len(suggestions))

		// Prompt for action
		action := a.promptForAction()

		// Process the action
		switch action {
		case "apply":
			if err := a.applySuggestion(selected); err != nil {
				fmt.Printf("%sFailed to apply: %v\n", ui.EmojiText("❌ ", ""), err)
				skipped++
			} else {
				fmt.Printf("%sApplied\n", ui.EmojiText("✅ ", ""))
				applied++
				a.showGitDiff(selected.Path)
				a.promptToResolveThread(selected)
			}
		case "ai":
			if a.aiProvider == nil {
				fmt.Printf("%sAI provider not configured\n", ui.EmojiText("❌ ", ""))
				skipped++
			} else {
				if err := a.applyWithAI(selected, false); err != nil {
					if err == errEditApplied {
						applied++
					} else {
						fmt.Printf("%sAI application failed: %v\n", ui.EmojiText("❌ ", ""), err)
						skipped++
					}
				} else {
					fmt.Printf("%sApplied with AI\n", ui.EmojiText("✅ ", ""))
					applied++
					a.showGitDiff(selected.Path)
					a.promptToResolveThread(selected)
				}
			}
		case "skip":
			fmt.Printf("%sSkipped\n", ui.EmojiText("⏭️  ", ""))
			skipped++
		case "quit":
			fmt.Printf("\nStopped. Applied %d/%d suggestions\n", applied, len(suggestions))
			return nil
		}

		// Remove the processed suggestion from remaining
		for i, s := range remaining {
			if s.ID == selected.ID {
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}

	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorGray, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
	fmt.Printf("%s Applied %s, Skipped %s\n",
		ui.Colorize(ui.ColorCyan, "Summary:"),
		ui.Colorize(ui.ColorGreen, fmt.Sprintf("%d", applied)),
		ui.Colorize(ui.ColorYellow, fmt.Sprintf("%d", skipped)))
	return nil
}

// showSuggestionDetails displays full details of a selected suggestion
func (a *Applier) showSuggestionDetails(suggestion *github.ReviewComment, index, total int) {
	fileLocation := fmt.Sprintf("%s:%d", suggestion.Path, suggestion.Line)
	clickableLocation := ui.CreateHyperlink(suggestion.HTMLURL, fileLocation)

	header := fmt.Sprintf("[%d/%d] %s by @%s", index, total, clickableLocation, suggestion.Author)
	if suggestion.IsOutdated {
		header += ui.Colorize(ui.ColorYellow, ui.EmojiText(" ⚠️  OUTDATED", " OUTDATED"))
	}
	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, header))
	fmt.Printf("%s\n", ui.Colorize(ui.ColorGray, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))

	// Show the review comment
	if commentText := ui.StripSuggestionBlock(suggestion.Body); commentText != "" {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorYellow, "Review comment:"))
		rendered, err := ui.RenderMarkdown(commentText)
		if err == nil && rendered != "" {
			fmt.Println(rendered)
		} else {
			wrappedComment := ui.WrapText(commentText, 80)
			fmt.Printf("%s\n", wrappedComment)
		}
	}

	// Show the suggestion
	fmt.Printf("\n%s\n", "Suggested change:")
	fmt.Println(ui.ColorizeCode(suggestion.SuggestedCode))

	// Show context
	if suggestion.DiffHunk != "" {
		fmt.Printf("\n%s\n", "Context:")
		fmt.Println(ui.ColorizeDiff(suggestion.DiffHunk))
	}

	// Show thread comments
	if len(suggestion.ThreadComments) > 0 {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Thread replies:"))
		for i, threadComment := range suggestion.ThreadComments {
			fmt.Printf("\n  %s\n", ui.Colorize(ui.ColorGray, fmt.Sprintf("└─ Reply %d by @%s:", i+1, threadComment.Author)))
			rendered, err := ui.RenderMarkdown(threadComment.Body)
			if err == nil && rendered != "" {
				lines := strings.Split(rendered, "\n")
				for _, line := range lines {
					fmt.Printf("     %s\n", line)
				}
			} else {
				wrappedReply := ui.WrapText(threadComment.Body, 75)
				lines := strings.Split(wrappedReply, "\n")
				for _, line := range lines {
					fmt.Printf("     %s\n", line)
				}
			}
		}
	}
}

// promptForAction prompts user for action on the selected suggestion
func (a *Applier) promptForAction() string {
	prompt := "Apply this suggestion? [y/s/q] (yes/skip/quit)"
	if a.aiProvider != nil {
		prompt = "Apply this suggestion? [y/s/a/q] (yes/skip/ai-apply/quit)"
	}

	for {
		fmt.Printf("\n%s ", prompt)
		var response string
		_, err := fmt.Scanln(&response)
		if err != nil {
			// If scanln fails (e.g., EOF), treat as quit
			return "quit"
		}

		response = strings.ToLower(strings.TrimSpace(response))

		switch response {
		case "y", "yes":
			return "apply"
		case "a", "ai", "ai-apply":
			if a.aiProvider != nil {
				return "ai"
			}
			fmt.Printf("%sAI provider not configured, please choose again\n", ui.EmojiText("❌ ", ""))
		case "s", "skip", "n", "no", "":
			return "skip"
		case "q", "quit":
			return "quit"
		default:
			fmt.Printf("%sUnrecognized input, skipping\n", ui.EmojiText("⏭️  ", ""))
			return "skip"
		}
	}
}

// applySuggestion applies a single suggestion to a file by directly modifying the content
func (a *Applier) applySuggestion(comment *github.ReviewComment) error {
	a.debugLog("Applying suggestion for comment ID=%d, Path=%s, Line=%d", comment.ID, comment.Path, comment.Line)

	if err := validatePath(comment.Path); err != nil {
		return err
	}

	// Read the current file
	fileContent, err := os.ReadFile(comment.Path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", comment.Path, err)
	}
	fileLines := strings.Split(string(fileContent), "\n")

	// Find the lines to replace
	targetLine, removeCount, err := a.findReplacementTarget(comment, fileLines)
	if err != nil {
		return err
	}

	a.debugLog("Replacing %d lines starting at line %d with suggested code", removeCount, targetLine+1)

	// Prepare the new lines
	suggestionLines := strings.Split(strings.TrimSuffix(comment.SuggestedCode, "\n"), "\n")

	// Construct the new file content
	var newFileLines []string

	// Add lines before the change
	newFileLines = append(newFileLines, fileLines[:targetLine]...)

	// Add the suggested lines
	newFileLines = append(newFileLines, suggestionLines...)

	// Add lines after the change
	if targetLine+removeCount < len(fileLines) {
		newFileLines = append(newFileLines, fileLines[targetLine+removeCount:]...)
	}

	// Join lines and write back to file
	// Note: This assumes \n line endings. For mixed line endings, we might want to detect the file's EOL.
	newContent := strings.Join(newFileLines, "\n")

	// Preserve trailing newline if the original file had one
	if strings.HasSuffix(string(fileContent), "\n") && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}

	if err := os.WriteFile(comment.Path, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", comment.Path, err)
	}

	a.debugLog("Successfully applied suggestion to %s", comment.Path)
	return nil
}

// findReplacementTargetByLineRange uses the comment's StartLine/Line fields
// to determine the replacement range. Used by Browse view actions.
func (a *Applier) findReplacementTargetByLineRange(comment *github.ReviewComment, fileLines []string) (int, int, error) {
	// Use the comment's line range fields to determine what to replace
	// StartLine and Line define the range of lines the suggestion applies to
	startLine := comment.StartLine
	endLine := comment.Line

	// If StartLine is 0, it's a single-line suggestion
	if startLine == 0 {
		startLine = endLine
	}

	a.debugLog("Suggestion targets lines %d-%d (1-based)", startLine, endLine)

	// Convert to 0-based index
	targetLine := startLine - 1
	removeCount := endLine - startLine + 1

	// Validate the range
	if targetLine < 0 || targetLine >= len(fileLines) {
		return -1, 0, fmt.Errorf("target line %d is out of bounds (file has %d lines)", startLine, len(fileLines))
	}
	if targetLine+removeCount > len(fileLines) {
		return -1, 0, fmt.Errorf("target range %d-%d exceeds file length (%d lines)", startLine, endLine, len(fileLines))
	}

	return targetLine, removeCount, nil
}

// findReplacementTarget identifies the start line and number of lines to replace.
// Uses a two-strategy approach: position mapping from the diff hunk, then content matching.
// Used by the "apply" subcommand.
func (a *Applier) findReplacementTarget(comment *github.ReviewComment, fileLines []string) (int, int, error) {
	// Extract the lines that were added in the PR (+ lines) from DiffHunk
	// These are the lines we expect to find in the local file and replace
	addedLines := diffhunk.GetAddedLines(comment.DiffHunk)

	if len(addedLines) == 0 {
		// If no added lines, this might be a pure addition (no replacement)
		// But GitHub suggestions usually replace something.
		// If it's a pure addition, we need to know where to insert.
		// For now, let's assume we need context.
		return -1, 0, fmt.Errorf("no added lines found in diff hunk - cannot determine what to replace")
	}

	// Strategy 1: Try using position mapping from the diff hunk
	targetLine := -1

	if comment.DiffHunk != "" {
		parsedHunk, parseErr := diffhunk.ParseDiffHunk(comment.DiffHunk)
		if parseErr == nil {
			// Use the first added line's position
			for _, line := range parsedHunk.Lines {
				if line.Type == diffhunk.Add {
					// Map from new file position to current file (0-based)
					targetLine = diffhunk.GetZeroBased(line.NewLineNumber)
					a.debugLog("Strategy 1 (position mapping): Found first added line at new position %d (0-based: %d)",
						line.NewLineNumber, targetLine)
					break
				}
			}
		}
	}

	// Strategy 2: Fall back to content matching if position mapping didn't work or verification fails
	// We verify Strategy 1 first. If it points to the wrong content, we discard it and try Strategy 2.
	strategy1Valid := false
	if targetLine != -1 {
		if targetLine+len(addedLines) <= len(fileLines) {
			match := true
			for j := 0; j < len(addedLines); j++ {
				if fileLines[targetLine+j] != addedLines[j] {
					match = false
					break
				}
			}
			if match {
				strategy1Valid = true
			} else {
				a.debugLog("Strategy 1 location found but content mismatch. Falling back to search.")
			}
		}
	}

	if !strategy1Valid {
		a.debugLog("Trying Strategy 2 (content matching)")

		matchStart := -1
		// Search for the block of lines
		for i := 0; i <= len(fileLines)-len(addedLines); i++ {
			match := true
			for j := 0; j < len(addedLines); j++ {
				if fileLines[i+j] != addedLines[j] {
					match = false
					break
				}
			}
			if match {
				matchStart = i
				a.debugLog("Strategy 2: Found content match at line %d (0-based)", matchStart)
				break
			}
		}

		if matchStart == -1 {
			return -1, 0, fmt.Errorf("could not find the code to replace in current file (looking for %d lines starting with %q)",
				len(addedLines), addedLines[0])
		}
		targetLine = matchStart
	}

	// Final verification (redundant if we just searched, but good for safety)
	if targetLine >= 0 && targetLine+len(addedLines) <= len(fileLines) {
		for j := 0; j < len(addedLines); j++ {
			if fileLines[targetLine+j] != addedLines[j] {
				mismatchLine := targetLine + j + 1
				diffFile := a.saveMismatchDiff(comment, fileLines, targetLine, addedLines, mismatchLine)
				if diffFile != "" {
					return -1, 0, fmt.Errorf("content mismatch at line %d - the code may have changed since the review\nDiagnostic diff saved to: %s", mismatchLine, diffFile)
				}
				return -1, 0, fmt.Errorf("content mismatch at line %d - the code may have changed since the review", mismatchLine)
			}
		}
	} else {
		return -1, 0, fmt.Errorf("target position %d is out of bounds (file has %d lines)", targetLine, len(fileLines))
	}

	return targetLine, len(addedLines), nil
}

// saveMismatchDiff creates a diagnostic diff file showing what was expected vs what was found
func (a *Applier) saveMismatchDiff(comment *github.ReviewComment, fileLines []string, targetLine int, expectedLines []string, mismatchLine int) string {
	var diff strings.Builder

	// Header
	fmt.Fprintf(&diff, "# Diagnostic diff for comment ID %d\n", comment.ID)
	fmt.Fprintf(&diff, "# File: %s\n", comment.Path)
	fmt.Fprintf(&diff, "# Comment URL: %s\n", comment.HTMLURL)
	fmt.Fprintf(&diff, "# Mismatch at line: %d\n", mismatchLine)
	fmt.Fprintf(&diff, "# Comment info: Line=%d, OriginalLine=%d, DiffSide=%s, IsOutdated=%v\n",
		comment.Line, comment.OriginalLine, comment.DiffSide, comment.IsOutdated)
	diff.WriteString("#\n")
	diff.WriteString("# Original diff hunk from GitHub:\n")
	for _, line := range strings.Split(comment.DiffHunk, "\n") {
		fmt.Fprintf(&diff, "# %s\n", line)
	}
	diff.WriteString("#\n")
	diff.WriteString("# EXPECTED (from GitHub review):\n")
	for i, line := range expectedLines {
		marker := " "
		if targetLine+i+1 == mismatchLine {
			marker = "!"
		}
		fmt.Fprintf(&diff, "# %s [%d] %s\n", marker, targetLine+i+1, line)
	}
	diff.WriteString("#\n")
	diff.WriteString("# ACTUAL (current file content):\n")
	for i := 0; i < len(expectedLines) && targetLine+i < len(fileLines); i++ {
		marker := " "
		if targetLine+i+1 == mismatchLine {
			marker = "!"
		}
		fmt.Fprintf(&diff, "# %s [%d] %s\n", marker, targetLine+i+1, fileLines[targetLine+i])
	}
	diff.WriteString("#\n")
	diff.WriteString("# Unified diff (proper format):\n")
	diff.WriteString("#\n")

	contextStart := targetLine - 5
	if contextStart < 0 {
		contextStart = 0
	}
	contextEnd := targetLine + len(expectedLines) + 5
	if contextEnd > len(fileLines) {
		contextEnd = len(fileLines)
	}

	fmt.Fprintf(&diff, "--- a/%s (expected based on review)\n", comment.Path)
	fmt.Fprintf(&diff, "+++ b/%s (actual current content)\n", comment.Path)
	fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n",
		targetLine+1, len(expectedLines),
		targetLine+1, len(expectedLines))

	// Show context before
	for i := contextStart; i < targetLine && i < len(fileLines); i++ {
		fmt.Fprintf(&diff, " %s\n", fileLines[i])
	}

	// Show the expected lines (what review expected - as removed)
	for i := 0; i < len(expectedLines); i++ {
		fmt.Fprintf(&diff, "-%s\n", expectedLines[i])
	}

	// Show the actual lines (what we found - as added)
	for i := targetLine; i < targetLine+len(expectedLines) && i < len(fileLines); i++ {
		fmt.Fprintf(&diff, "+%s\n", fileLines[i])
	}

	// Show context after
	for i := targetLine + len(expectedLines); i < contextEnd && i < len(fileLines); i++ {
		fmt.Fprintf(&diff, " %s\n", fileLines[i])
	}

	diff.WriteString("\n#\n")
	diff.WriteString("# Suggested change from review:\n")
	diff.WriteString("#\n")
	for _, line := range strings.Split(comment.SuggestedCode, "\n") {
		fmt.Fprintf(&diff, "# > %s\n", line)
	}

	f, err := os.CreateTemp("", "gh-review-responder-mismatch-*.diff")
	if err != nil {
		a.debugLog("Failed to create mismatch diff temp file: %v", err)
		return ""
	}
	name := f.Name()

	if _, err := f.WriteString(diff.String()); err != nil {
		_ = f.Close()
		a.debugLog("Failed to write mismatch diff: %v", err)
		return ""
	}
	_ = f.Close()

	a.debugLog("Saved diagnostic diff to: %s", name)
	return name
}

// showGitDiff shows the git diff for a file after applying changes
func (a *Applier) showGitDiff(filePath string) {
	args := []string{"diff"}
	if ui.ColorsEnabled() {
		args = append(args, "--color=always")
	} else {
		args = append(args, "--color=never")
	}
	args = append(args, filePath)

	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Don't fail, just skip showing diff
		return
	}

	if len(output) > 0 && strings.TrimSpace(string(output)) != "" {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Applied changes:"))
		fmt.Print(string(output))
	}
}

// applyWithAI uses AI to apply a suggestion intelligently
func (a *Applier) applyWithAI(comment *github.ReviewComment, autoApply bool) error {
	ctx := context.Background()

	if err := validatePath(comment.Path); err != nil {
		return err
	}

	// Read current file
	fileContent, err := os.ReadFile(comment.Path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Extract expected lines from diff hunk
	expectedLines := diffhunk.GetAddedLines(comment.DiffHunk)

	// Detect language from file extension
	language := detectLanguage(comment.Path)

	// Build AI request
	req := &ai.SuggestionRequest{
		ReviewComment:      comment.Body,
		SuggestedCode:      comment.SuggestedCode,
		OriginalDiffHunk:   comment.DiffHunk,
		CommentID:          comment.ID,
		FilePath:           comment.Path,
		CurrentFileContent: string(fileContent),
		TargetLineNumber:   comment.Line - 1, // 0-based
		ExpectedLines:      expectedLines,
		FileLanguage:       language,
	}

	providerName := a.aiProvider.Name()
	modelName := a.aiProvider.Model()
	fmt.Printf("\n%s%s\n", ui.EmojiText("🤖 ", ""), ui.Colorize(ui.ColorCyan, fmt.Sprintf("Using AI to apply suggestion (%s/%s)...", providerName, modelName)))

	// Create and start spinner
	s := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
	s.Suffix = fmt.Sprintf(" Analyzing code and generating patch with %s (%s)...", providerName, modelName)
	s.Start()

	// Call AI provider
	resp, err := a.aiProvider.ApplySuggestion(ctx, req)

	// Stop spinner
	s.Stop()

	if err != nil {
		return fmt.Errorf("AI provider error: %w", err)
	}

	// Show AI's explanation
	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "AI Analysis:"))
	fmt.Printf("%s\n", resp.Explanation)

	if len(resp.Warnings) > 0 {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorYellow, ui.EmojiText("⚠️  Warnings:", "Warnings:")))
		for _, warning := range resp.Warnings {
			fmt.Printf("  • %s\n", warning)
		}
	}

	fmt.Printf("\nConfidence: %.0f%%\n", resp.Confidence*100)

	// Show the generated patch
	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Generated patch:"))
	fmt.Println(ui.ColorizeDiff(resp.Patch))

	a.debugLog("AI-generated patch:\n%s", resp.Patch)

	// Ask for confirmation (unless auto-apply mode)
	patchToApply := resp.Patch
	if !autoApply {
		reader := bufio.NewReader(os.Stdin)
	confirmationLoop:
		for {
			fmt.Printf("\n%s ", ui.Colorize(ui.ColorYellow, "Apply this AI-generated patch? [y/n/e] (yes/no/edit)"))
			response, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}

			response = strings.ToLower(strings.TrimSpace(response))

			switch response {
			case "y", "yes":
				// Continue to apply
				break confirmationLoop
			case "n", "no":
				return fmt.Errorf("AI patch application cancelled by user")
			case "e", "edit":
				// Apply patch and open file for editing
				if err := a.applyPatchAndEditFile(patchToApply, comment.Path, comment); err != nil {
					fmt.Printf("%sFailed to apply and edit: %v\n", ui.EmojiText("❌ ", ""), err)
					// Ask if they want to try with original patch
					fmt.Printf("Try applying without editing? [y/n] ")
					continueResp, _ := reader.ReadString('\n')
					continueResp = strings.ToLower(strings.TrimSpace(continueResp))
					if continueResp == "y" || continueResp == "yes" {
						break confirmationLoop
					}
					return fmt.Errorf("AI patch application cancelled by user")
				}
				// Successfully applied and edited
				return errEditApplied
			default:
				fmt.Printf("Invalid input. Please enter y, n, or e.\n")
			}
		}
	}

	// Apply the AI-generated patch
	cmd := exec.Command("git", "apply", "--unidiff-zero", "-")
	cmd.Stdin = strings.NewReader(patchToApply)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Save failed AI patch for debugging
		var patchContent strings.Builder
		fmt.Fprintf(&patchContent, "# AI-generated patch for comment ID %d\n", comment.ID)
		fmt.Fprintf(&patchContent, "# File: %s\n", comment.Path)
		fmt.Fprintf(&patchContent, "# AI Provider: %s\n", a.aiProvider.Name())
		fmt.Fprintf(&patchContent, "# Confidence: %.0f%%\n", resp.Confidence*100)
		fmt.Fprintf(&patchContent, "# Error: %v\n", err)
		fmt.Fprintf(&patchContent, "# git apply output:\n")
		for _, line := range strings.Split(string(output), "\n") {
			fmt.Fprintf(&patchContent, "# %s\n", line)
		}
		fmt.Fprintf(&patchContent, "#\n# Generated patch:\n#\n")
		patchContent.WriteString(resp.Patch)

		patchFile, tmpErr := os.CreateTemp("", "gh-review-responder-ai-patch-*.patch")
		if tmpErr != nil {
			a.debugLog("Failed to create AI patch temp file: %v", tmpErr)
			return fmt.Errorf("failed to apply AI-generated patch: %w\nOutput: %s", err, string(output))
		}
		patchFileName := patchFile.Name()
		_, writeErr := patchFile.WriteString(patchContent.String())
		_ = patchFile.Close()
		if writeErr != nil {
			a.debugLog("Failed to write AI patch to %s: %v", patchFileName, writeErr)
		}

		return fmt.Errorf("failed to apply AI-generated patch (saved to %s): %w\nOutput: %s",
			patchFileName, err, string(output))
	}

	return nil
}

// applyPatchAndEditFile applies a patch and then opens the file for further editing
func (a *Applier) applyPatchAndEditFile(patch string, filePath string, comment *github.ReviewComment) error {
	// First, apply the patch
	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Applying patch to file..."))
	cmd := exec.Command("git", "apply", "--unidiff-zero", "-")
	cmd.Stdin = strings.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to apply patch: %w\nOutput: %s", err, string(output))
	}

	fmt.Printf("%sPatch applied. Opening file for additional edits...\n", ui.EmojiText("✅ ", ""))

	// Get editor from environment, default to vi
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	// Open the file in editor
	editorParts := strings.Fields(editor)
	editorCmd := exec.Command(editorParts[0], append(editorParts[1:], filePath)...)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr

	if err := editorCmd.Run(); err != nil {
		// Editor failed, revert the patch
		fmt.Printf("%sEditor exited with error: %v\n", ui.EmojiText("❌ ", ""), err)
		fmt.Printf("Reverting changes...\n")
		revertCmd := exec.Command("git", "checkout", "--", filePath)
		if revertErr := revertCmd.Run(); revertErr != nil {
			fmt.Printf("%sFailed to revert changes: %v\n", ui.EmojiText("❌ ", ""), revertErr)
			return fmt.Errorf("editor failed and revert failed: %w", revertErr)
		}
		return fmt.Errorf("editor failed")
	}

	// Show the diff of all changes (AI patch + user edits)
	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorCyan, "Final changes:"))
	a.showGitDiff(filePath)

	// Ask if they want to keep the changes
	fmt.Printf("\n%s ", ui.Colorize(ui.ColorYellow, "Keep these changes? [y/n]"))
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		// Revert on error
		revertCmd := exec.Command("git", "checkout", "--", filePath)
		if revertErr := revertCmd.Run(); revertErr != nil {
			fmt.Printf("%sFailed to revert changes: %v\n", ui.EmojiText("❌ ", ""), revertErr)
			return fmt.Errorf("failed to revert changes: %w", revertErr)
		}
		return fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.ToLower(strings.TrimSpace(response))
	if response != "y" && response != "yes" {
		// Revert the changes
		fmt.Printf("Reverting changes...\n")
		revertCmd := exec.Command("git", "checkout", "--", filePath)
		if err := revertCmd.Run(); err != nil {
			return fmt.Errorf("failed to revert changes: %w", err)
		}
		fmt.Printf("%sChanges reverted\n", ui.EmojiText("❌ ", ""))
		return fmt.Errorf("changes discarded by user")
	}

	fmt.Printf("%sChanges kept\n", ui.EmojiText("✅ ", ""))

	// Prompt to resolve thread
	a.promptToResolveThread(comment)

	return nil
}

// promptToResolveThread asks user if they want to mark the review thread as resolved
func (a *Applier) promptToResolveThread(comment *github.ReviewComment) {
	// Only prompt if we have a GitHub client and thread ID
	if a.githubClient == nil || comment.ThreadID == "" {
		return
	}

	// Don't prompt if already resolved
	if comment.IsResolved() {
		return
	}

	fmt.Printf("\n%s ", ui.Colorize(ui.ColorYellow, "Mark this review thread as resolved? [y/n]"))
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return
	}

	response = strings.ToLower(strings.TrimSpace(response))
	if response == "y" || response == "yes" {
		if err := a.githubClient.ResolveThread(comment.ThreadID); err != nil {
			fmt.Printf("%sFailed to resolve thread: %v\n", ui.EmojiText("❌ ", ""), err)
		} else {
			fmt.Printf("%sReview thread marked as resolved\n", ui.EmojiText("✅ ", ""))
		}
	}
}

// ApplyAllWithAI applies all suggestions using AI without prompting
func (a *Applier) ApplyAllWithAI(suggestions []*github.ReviewComment) error {
	if a.aiProvider == nil {
		return fmt.Errorf("AI provider not configured")
	}

	applied := 0
	failed := 0

	for _, suggestion := range suggestions {
		fmt.Printf("\n%s\n", ui.Colorize(ui.ColorGray, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
		fmt.Printf("%s %s:%d by @%s\n",
			ui.Colorize(ui.ColorCyan, "Processing:"),
			suggestion.Path, suggestion.Line, suggestion.Author)

		if err := a.applyWithAI(suggestion, true); err != nil {
			fmt.Printf("%sFailed: %v\n", ui.EmojiText("❌ ", ""), err)
			failed++
		} else {
			fmt.Printf("%sApplied successfully\n", ui.EmojiText("✅ ", ""))
			applied++

			// Show git diff of what was applied
			a.showGitDiff(suggestion.Path)

			// Automatically resolve thread when possible
			if a.githubClient != nil && suggestion.ThreadID != "" && !suggestion.IsResolved() {
				if err := a.githubClient.ResolveThread(suggestion.ThreadID); err != nil {
					fmt.Printf("%sFailed to auto-resolve thread: %v\n", ui.EmojiText("⚠️  ", ""), err)
				} else {
					fmt.Printf("%sReview thread auto-resolved\n", ui.EmojiText("✅ ", ""))
				}
			}
		}
	}

	fmt.Printf("\n%s\n", ui.Colorize(ui.ColorGray, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
	fmt.Printf("%s Applied %s, Failed %s\n",
		ui.Colorize(ui.ColorCyan, "Summary:"),
		ui.Colorize(ui.ColorGreen, fmt.Sprintf("%d", applied)),
		ui.Colorize(ui.ColorRed, fmt.Sprintf("%d", failed)))
	return nil
}

// detectLanguage detects programming language from file extension
func detectLanguage(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".jsx":
		return "javascript"
	case ".tsx":
		return "typescript"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".h", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".sh":
		return "bash"
	case ".md":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	default:
		return "unknown"
	}
}

// suggestionRenderer implements ui.ItemRenderer for ReviewComments in the apply context
type suggestionRenderer struct {
	aiAvailable bool
}

func (r *suggestionRenderer) Title(comment *github.ReviewComment) string {
	style := ui.NewSuggestionListStyle(comment.Author, comment.IsResolved())
	return style.FormatSuggestionTitle(comment.Path, comment.Line)
}

func (r *suggestionRenderer) Description(comment *github.ReviewComment) string {
	style := ui.NewSuggestionListStyle(comment.Author, comment.IsResolved())
	return style.FormatSuggestionDescription(comment.HasSuggestion, comment.IsOutdated)
}

func (r *suggestionRenderer) Preview(comment *github.ReviewComment) string {
	return r.PreviewWithHighlight(comment, -1) // No highlight
}

func (r *suggestionRenderer) PreviewWithHighlight(comment *github.ReviewComment, highlightIdx int) string {
	var preview strings.Builder
	maxLines := 20 // Limit preview to fit screen

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

	if comment.IsOutdated {
		preview.WriteString(ui.Colorize(ui.ColorYellow, ui.EmojiText("⚠️  OUTDATED\n", "OUTDATED\n")))
	}

	if r.aiAvailable {
		preview.WriteString(ui.Colorize(ui.ColorGreen, ui.EmojiText("🤖 AI available\n", "AI available\n")))
	}

	lines := strings.Count(preview.String(), "\n") + 1

	// Review comment (truncated)
	body := ui.StripSuggestionBlock(comment.Body)
	if body != "" && lines < maxLines {
		preview.WriteString("\n--- Comment ---\n")
		bodyLines := strings.Split(body, "\n")
		for _, line := range bodyLines {
			if lines >= maxLines-2 {
				preview.WriteString("...\n")
				break
			}
			preview.WriteString(line + "\n")
			lines++
		}
	}

	// Suggested code (truncated)
	if comment.HasSuggestion && comment.SuggestedCode != "" && lines < maxLines {
		preview.WriteString(ui.Colorize(ui.ColorCyan, "\n--- Suggested Code ---\n"))
		codeLines := strings.Split(comment.SuggestedCode, "\n")
		shown := 0
		for _, line := range codeLines {
			if lines >= maxLines-2 || shown >= 6 {
				preview.WriteString(ui.Colorize(ui.ColorGray, "...\n"))
				break
			}
			preview.WriteString(ui.Colorize(ui.ColorGreen, line+"\n"))
			lines++
			shown++
		}
	}

	// Diff hunk/context (truncated with coloring) - only if substantial
	if comment.DiffHunk != "" && lines < maxLines {
		diffLines := strings.Split(comment.DiffHunk, "\n")
		// Only show context if it has more than just the header
		if len(diffLines) > 2 {
			preview.WriteString(ui.Colorize(ui.ColorCyan, "\n--- Context ---\n"))
			shown := 0
			for _, line := range diffLines {
				if lines >= maxLines-2 || shown >= 5 {
					preview.WriteString(ui.Colorize(ui.ColorGray, "...\n"))
					break
				}
				// Color diff lines
				coloredLine := line
				if len(line) > 0 {
					switch line[0] {
					case '+':
						coloredLine = ui.Colorize(ui.ColorGreen, line)
					case '-':
						coloredLine = ui.Colorize(ui.ColorRed, line)
					case '@':
						coloredLine = ui.Colorize(ui.ColorCyan, line)
					default:
						coloredLine = ui.Colorize(ui.ColorGray, line)
					}
				}
				preview.WriteString(coloredLine + "\n")
				lines++
				shown++
			}
		}
	}

	// Thread replies (just summary)
	if len(comment.ThreadComments) > 0 && lines < maxLines {
		fmt.Fprintf(&preview, "\n--- %d Replies ---\n", len(comment.ThreadComments))
		for i, threadComment := range comment.ThreadComments {
			if lines >= maxLines-1 {
				preview.WriteString("...\n")
				break
			}
			fmt.Fprintf(&preview, "Reply %d by @%s\n", i+1, threadComment.Author)
			lines++
		}
	}

	return preview.String()
}

func (r *suggestionRenderer) EditPath(comment *github.ReviewComment) string {
	return comment.Path
}

func (r *suggestionRenderer) EditLine(comment *github.ReviewComment) int {
	return comment.Line
}

func (r *suggestionRenderer) FilterValue(comment *github.ReviewComment) string {
	parts := []string{
		r.Title(comment),
		r.Description(comment),
		comment.Path,
	}

	if body := ui.StripSuggestionBlock(comment.Body); body != "" {
		parts = append(parts, body)
	}

	if comment.SuggestedCode != "" {
		parts = append(parts, comment.SuggestedCode)
	}

	if comment.DiffHunk != "" {
		parts = append(parts, comment.DiffHunk)
	}

	for _, reply := range comment.ThreadComments {
		if reply.Body != "" {
			parts = append(parts, reply.Body)
		}
	}

	return strings.Join(parts, " ")
}

func (r *suggestionRenderer) IsSkippable(comment *github.ReviewComment) bool {
	return false
}

func (r *suggestionRenderer) ThreadCommentCount(comment *github.ReviewComment) int {
	return 0 // Not used in applier context
}

func (r *suggestionRenderer) ThreadCommentPreview(comment *github.ReviewComment, idx int) string {
	return "" // Not used in applier context
}

func (r *suggestionRenderer) WithSelectedComment(comment *github.ReviewComment, idx int) *github.ReviewComment {
	return comment // No-op in applier context
}
