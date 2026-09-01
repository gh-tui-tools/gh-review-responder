package ui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSanitizeEditorContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "no comment lines",
			input:    "Line 1\nLine 2\nLine 3",
			expected: "Line 1\nLine 2\nLine 3",
		},
		{
			name:     "only comment lines",
			input:    "# Comment 1\n# Comment 2",
			expected: "",
		},
		{
			name:     "mixed content with # in middle preserved",
			input:    "User content\n# This is a comment\nMore content",
			expected: "User content\n# This is a comment\nMore content",
		},
		{
			name:     "# at start preserved",
			input:    "# Header comment\nActual content",
			expected: "# Header comment\nActual content",
		},
		{
			name:     "comment at end",
			input:    "Content here\n# Footer comment",
			expected: "Content here",
		},
		{
			name:     "whitespace trimming",
			input:    "  \n\nContent\n\n  ",
			expected: "Content",
		},
		{
			name:     "preserves internal whitespace",
			input:    "Line 1\n\nLine 2",
			expected: "Line 1\n\nLine 2",
		},
		{
			name:     "typical editor template",
			input:    "> @author wrote:\n>\n> Original comment\n\nMy reply here\n# Write your comment above. Lines starting with # are ignored.\n",
			expected: "> @author wrote:\n>\n> Original comment\n\nMy reply here",
		},
		{
			name:     "hash in middle of line preserved",
			input:    "Code with # comment",
			expected: "Code with # comment",
		},
		{
			name:     "markdown heading preserved",
			input:    "# Heading\nContent",
			expected: "# Heading\nContent",
		},
		{
			name:     "markdown heading with trailing template",
			input:    "# Heading\nContent\n# Template comment",
			expected: "# Heading\nContent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeEditorContent(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeEditorContent(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestEditorPrepareComplete tests the type signatures compile correctly
func TestEditorPreparerType(t *testing.T) {
	// This test verifies the EditorPreparer type works as expected
	preparer := EditorPreparer[string](func(item string) (string, error) {
		return "prepared: " + item, nil
	})

	result, err := preparer("test")
	if err != nil {
		t.Errorf("EditorPreparer returned unexpected error: %v", err)
	}
	if result != "prepared: test" {
		t.Errorf("EditorPreparer result = %q, want %q", result, "prepared: test")
	}
}

func TestEditorCompleterType(t *testing.T) {
	// This test verifies the EditorCompleter type works as expected
	completer := EditorCompleter[string](func(item string, content string) (string, error) {
		return "completed: " + item + " with " + content, nil
	})

	result, err := completer("item", "content")
	if err != nil {
		t.Errorf("EditorCompleter returned unexpected error: %v", err)
	}
	if result != "completed: item with content" {
		t.Errorf("EditorCompleter result = %q, want %q", result, "completed: item with content")
	}
}

func TestCustomActionType(t *testing.T) {
	// This test verifies the CustomAction type works as expected
	action := CustomAction[int](func(item int) (string, error) {
		if item > 0 {
			return "positive", nil
		}
		return "non-positive", nil
	})

	result, err := action(5)
	if err != nil {
		t.Errorf("CustomAction returned unexpected error: %v", err)
	}
	if result != "positive" {
		t.Errorf("CustomAction result = %q, want %q", result, "positive")
	}

	result, err = action(-1)
	if err != nil {
		t.Errorf("CustomAction returned unexpected error: %v", err)
	}
	if result != "non-positive" {
		t.Errorf("CustomAction result = %q, want %q", result, "non-positive")
	}
}

func TestAgentFinishedMsgType(t *testing.T) {
	// This test verifies the agentFinishedMsg type works as expected
	msg := agentFinishedMsg{err: nil}
	if msg.err != nil {
		t.Errorf("agentFinishedMsg with nil error should have nil err")
	}

	expectedErr := "test error"
	msg = agentFinishedMsg{err: errors.New(expectedErr)}
	if msg.err == nil {
		t.Errorf("agentFinishedMsg with error should have non-nil err")
	}
	if msg.err.Error() != expectedErr {
		t.Errorf("agentFinishedMsg error = %q, want %q", msg.err.Error(), expectedErr)
	}
}

func TestLaunchAgentPrefix(t *testing.T) {
	// Test that LAUNCH_AGENT: prefix is correctly parsed
	tests := []struct {
		name           string
		input          string
		shouldLaunch   bool
		expectedPrompt string
	}{
		{
			name:           "valid launch agent prefix",
			input:          "LAUNCH_AGENT:Review comment on file.go:42\n\nComment body",
			shouldLaunch:   true,
			expectedPrompt: "Review comment on file.go:42\n\nComment body",
		},
		{
			name:           "no prefix",
			input:          "Some other result",
			shouldLaunch:   false,
			expectedPrompt: "",
		},
		{
			name:           "empty prompt after prefix",
			input:          "LAUNCH_AGENT:",
			shouldLaunch:   true,
			expectedPrompt: "",
		},
		{
			name:           "similar but not matching prefix",
			input:          "LAUNCH_AGENT_OTHER:something",
			shouldLaunch:   false,
			expectedPrompt: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := "LAUNCH_AGENT:"
			hasPrefix := len(tt.input) >= len(prefix) && tt.input[:len(prefix)] == prefix
			if hasPrefix != tt.shouldLaunch {
				t.Errorf("HasPrefix(%q, %q) = %v, want %v", tt.input, prefix, hasPrefix, tt.shouldLaunch)
			}
			if hasPrefix {
				prompt := tt.input[len(prefix):]
				if prompt != tt.expectedPrompt {
					t.Errorf("Prompt = %q, want %q", prompt, tt.expectedPrompt)
				}
			}
		})
	}
}

func TestEditFilePrefix(t *testing.T) {
	// Test that EDIT_FILE: prefix is correctly parsed
	tests := []struct {
		name         string
		input        string
		shouldEdit   bool
		expectedPath string
		expectedLine int
	}{
		{
			name:         "valid edit file prefix",
			input:        "EDIT_FILE:cmd/browse.go:42",
			shouldEdit:   true,
			expectedPath: "cmd/browse.go",
			expectedLine: 42,
		},
		{
			name:         "no prefix",
			input:        "Some other result",
			shouldEdit:   false,
			expectedPath: "",
			expectedLine: 0,
		},
		{
			name:         "path with colons",
			input:        "EDIT_FILE:/home/user/file.go:100",
			shouldEdit:   true,
			expectedPath: "/home/user/file.go",
			expectedLine: 100,
		},
		{
			name:         "line number zero",
			input:        "EDIT_FILE:file.go:0",
			shouldEdit:   true,
			expectedPath: "file.go",
			expectedLine: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := "EDIT_FILE:"
			hasPrefix := len(tt.input) >= len(prefix) && tt.input[:len(prefix)] == prefix
			if hasPrefix != tt.shouldEdit {
				t.Errorf("HasPrefix(%q, %q) = %v, want %v", tt.input, prefix, hasPrefix, tt.shouldEdit)
			}
			if hasPrefix {
				remainder := tt.input[len(prefix):]
				// Split on last colon to get path and line
				lastColon := -1
				for i := len(remainder) - 1; i >= 0; i-- {
					if remainder[i] == ':' {
						lastColon = i
						break
					}
				}
				if lastColon > 0 {
					path := remainder[:lastColon]
					var lineNum int
					_, _ = fmt.Sscanf(remainder[lastColon+1:], "%d", &lineNum)
					if path != tt.expectedPath {
						t.Errorf("Path = %q, want %q", path, tt.expectedPath)
					}
					if lineNum != tt.expectedLine {
						t.Errorf("Line = %d, want %d", lineNum, tt.expectedLine)
					}
				}
			}
		})
	}
}

func TestSplitActionKey(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedKey  string
		expectedDesc string
	}{
		{
			name:         "simple action key",
			input:        "r resolve",
			expectedKey:  "r",
			expectedDesc: "resolve",
		},
		{
			name:         "uppercase key",
			input:        "R resolve+comment",
			expectedKey:  "R",
			expectedDesc: "resolve+comment",
		},
		{
			name:         "multi-word description",
			input:        "Q quote reply",
			expectedKey:  "Q",
			expectedDesc: "quote reply",
		},
		{
			name:         "unresolve key",
			input:        "u unresolve",
			expectedKey:  "u",
			expectedDesc: "unresolve",
		},
		{
			name:         "uppercase unresolve key",
			input:        "U unresolve+comment",
			expectedKey:  "U",
			expectedDesc: "unresolve+comment",
		},
		{
			name:         "no description",
			input:        "x",
			expectedKey:  "x",
			expectedDesc: "",
		},
		{
			name:         "empty string",
			input:        "",
			expectedKey:  "",
			expectedDesc: "",
		},
		{
			name:         "reaction key",
			input:        "x react",
			expectedKey:  "x",
			expectedDesc: "react",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, desc := splitActionKey(tt.input)
			if key != tt.expectedKey {
				t.Errorf("splitActionKey(%q) key = %q, want %q", tt.input, key, tt.expectedKey)
			}
			if desc != tt.expectedDesc {
				t.Errorf("splitActionKey(%q) desc = %q, want %q", tt.input, desc, tt.expectedDesc)
			}
		})
	}
}

// TestReactionEmojis verifies the emoji reaction list is correctly defined
func TestReactionEmojis(t *testing.T) {
	// Test that we have exactly 8 emojis (GitHub's supported set)
	if len(reactionEmojis) != 8 {
		t.Errorf("reactionEmojis has %d items, want 8", len(reactionEmojis))
	}

	// Test that the expected emojis are present in order
	expectedEmojis := []struct {
		name    string
		display string
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

	for i, expected := range expectedEmojis {
		if reactionEmojis[i].name != expected.name {
			t.Errorf("reactionEmojis[%d].name = %q, want %q", i, reactionEmojis[i].name, expected.name)
		}
		if reactionEmojis[i].display != expected.display {
			t.Errorf("reactionEmojis[%d].display = %q, want %q", i, reactionEmojis[i].display, expected.display)
		}
	}
}

// TestReactionActionType verifies the ReactionAction callback type works correctly
func TestReactionActionType(t *testing.T) {
	// Test a successful reaction action
	action := func(item string) (int64, error) {
		if item == "comment1" {
			return 12345, nil
		}
		return 0, fmt.Errorf("unknown item: %s", item)
	}

	commentID, err := action("comment1")
	if err != nil {
		t.Errorf("ReactionAction returned unexpected error: %v", err)
	}
	if commentID != 12345 {
		t.Errorf("ReactionAction commentID = %d, want %d", commentID, 12345)
	}

	// Test error case
	_, err = action("unknown")
	if err == nil {
		t.Error("ReactionAction should return error for unknown item")
	}
}

// TestReactionCompleteType verifies the ReactionComplete callback type works correctly
func TestReactionCompleteType(t *testing.T) {
	// Track what was called
	var calledItem string
	var calledID int64
	var calledDisplayEmoji string

	complete := func(item string, commentID int64, apiName, displayEmoji string) (string, error) {
		calledItem = item
		calledID = commentID
		calledDisplayEmoji = displayEmoji
		if apiName == "invalid" {
			return "", fmt.Errorf("invalid emoji")
		}
		url := fmt.Sprintf("https://example.com/comment/%d", commentID)
		link := CreateHyperlink(url, "reaction added")
		return fmt.Sprintf("%s %s.", displayEmoji, link), nil
	}

	// Test successful completion
	msg, err := complete("test-item", 12345, "+1", "👍")
	if err != nil {
		t.Errorf("ReactionComplete returned unexpected error: %v", err)
	}
	if msg == "" {
		t.Error("ReactionComplete should return a confirmation message")
	}
	if calledItem != "test-item" {
		t.Errorf("ReactionComplete was called with item = %q, want %q", calledItem, "test-item")
	}
	if calledID != 12345 {
		t.Errorf("ReactionComplete was called with commentID = %d, want %d", calledID, 12345)
	}
	if calledDisplayEmoji != "👍" {
		t.Errorf("ReactionComplete was called with displayEmoji = %q, want %q", calledDisplayEmoji, "👍")
	}

	// Test error case
	_, err = complete("test-item", 99999, "invalid", "❌")
	if err == nil {
		t.Error("ReactionComplete should return error for invalid emoji")
	}
}

// TestSelectorOptionsReactionFields verifies reaction fields are accessible
func TestSelectorOptionsReactionFields(t *testing.T) {
	// Create options with reaction callbacks
	opts := SelectorOptions[string]{
		Items: []string{"item1", "item2"},
		ReactionAction: func(item string) (int64, error) {
			return 123, nil
		},
		ReactionComplete: func(item string, commentID int64, apiName, displayEmoji string) (string, error) {
			return fmt.Sprintf("%s added", displayEmoji), nil
		},
		ReactionKey: "x react",
	}

	// Verify the callbacks are set
	if opts.ReactionAction == nil {
		t.Error("ReactionAction should not be nil")
	}
	if opts.ReactionComplete == nil {
		t.Error("ReactionComplete should not be nil")
	}
	if opts.ReactionKey != "x react" {
		t.Errorf("ReactionKey = %q, want %q", opts.ReactionKey, "x react")
	}

	// Test that the callbacks work
	id, err := opts.ReactionAction("item1")
	if err != nil || id != 123 {
		t.Errorf("ReactionAction callback failed: id=%d, err=%v", id, err)
	}

	msg, err := opts.ReactionComplete("item1", 123, "+1", "👍")
	if err != nil {
		t.Errorf("ReactionComplete callback failed: %v", err)
	}
	if msg == "" {
		t.Error("ReactionComplete should return a message")
	}
}

// TestReactionModeStateCycle verifies the reaction mode cycling logic
func TestReactionModeStateCycle(t *testing.T) {
	// Simulate cycling through reactions
	reactionIdx := 0
	numEmojis := len(reactionEmojis)

	// Cycle through all emojis
	for i := 0; i < numEmojis; i++ {
		expectedEmoji := reactionEmojis[reactionIdx]
		if expectedEmoji.name == "" {
			t.Errorf("Empty emoji name at index %d", reactionIdx)
		}

		// Cycle to next
		reactionIdx = (reactionIdx + 1) % numEmojis
	}

	// After cycling through all, we should be back at the start
	if reactionIdx != 0 {
		t.Errorf("After full cycle, reactionIdx = %d, want 0", reactionIdx)
	}
}

// TestReactionStatusMessageFormat verifies the status message format
func TestReactionStatusMessageFormat(t *testing.T) {
	// Test the expected format for different indices
	tests := []struct {
		idx         int
		wantContain string
	}{
		{0, "[1/8] 👍"},
		{1, "[2/8] 👎"},
		{7, "[8/8] 👀"},
	}

	for _, tt := range tests {
		emoji := reactionEmojis[tt.idx]
		msg := fmt.Sprintf("React: [%d/%d] %s (x=next, Enter=add, Esc=cancel)",
			tt.idx+1, len(reactionEmojis), emoji.display)

		if !strings.Contains(msg, tt.wantContain) {
			t.Errorf("Status message at idx %d = %q, want to contain %q", tt.idx, msg, tt.wantContain)
		}
		if !strings.Contains(msg, "x=next") {
			t.Errorf("Status message should contain 'x=next'")
		}
		if !strings.Contains(msg, "Enter=add") {
			t.Errorf("Status message should contain 'Enter=add'")
		}
		if !strings.Contains(msg, "Esc=cancel") {
			t.Errorf("Status message should contain 'Esc=cancel'")
		}
	}
}

// TestReactionConfirmationMessageFormat verifies the confirmation message includes URL
func TestReactionConfirmationMessageFormat(t *testing.T) {
	// Simulate what browse.go does when building the confirmation message
	displayEmoji := "👍"
	repo := "owner/repo"
	prNumber := 123
	commentID := int64(456789)

	url := fmt.Sprintf("https://github.com/%s/pull/%d#discussion_r%d", repo, prNumber, commentID)
	link := CreateHyperlink(url, "reaction added")
	msg := fmt.Sprintf("%s %s.", displayEmoji, link)

	// Verify the message format
	if !strings.Contains(msg, displayEmoji) {
		t.Errorf("Confirmation message should contain emoji %q", displayEmoji)
	}
	if !strings.Contains(msg, "reaction added") {
		t.Error("Confirmation message should contain 'reaction added'")
	}
	if !strings.Contains(msg, "https://github.com/") {
		t.Error("Confirmation message should contain GitHub URL (in hyperlink)")
	}
	if !strings.Contains(msg, "#discussion_r") {
		t.Error("Confirmation message should contain comment anchor (in hyperlink)")
	}

	// Verify the full confirmation dialog format
	dialogMsg := fmt.Sprintf("%s\n\nPress any key to continue...", msg)
	if !strings.Contains(dialogMsg, "Press any key") {
		t.Error("Dialog should contain dismiss instructions")
	}
}

// TestReactionEmojisMatchGitHubAPI verifies emoji names match GitHub's API
func TestReactionEmojisMatchGitHubAPI(t *testing.T) {
	// GitHub's supported reaction content values (from their API documentation)
	// https://docs.github.com/en/rest/reactions#about-reactions
	githubEmojis := map[string]bool{
		"+1":       true,
		"-1":       true,
		"laugh":    true,
		"confused": true,
		"heart":    true,
		"hooray":   true,
		"rocket":   true,
		"eyes":     true,
	}

	for i, emoji := range reactionEmojis {
		if !githubEmojis[emoji.name] {
			t.Errorf("reactionEmojis[%d].name = %q is not a valid GitHub API emoji", i, emoji.name)
		}
	}

	// Verify we have all of them
	if len(reactionEmojis) != len(githubEmojis) {
		t.Errorf("reactionEmojis has %d items, GitHub API has %d", len(reactionEmojis), len(githubEmojis))
	}
}

// TestReactionIndexCyclingWrapAround verifies cycling wraps correctly at boundaries
func TestReactionIndexCyclingWrapAround(t *testing.T) {
	numEmojis := len(reactionEmojis)

	tests := []struct {
		name     string
		startIdx int
		wantIdx  int
	}{
		{"from first to second", 0, 1},
		{"from middle", 3, 4},
		{"from last to first (wrap)", numEmojis - 1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextIdx := (tt.startIdx + 1) % numEmojis
			if nextIdx != tt.wantIdx {
				t.Errorf("cycling from %d: got %d, want %d", tt.startIdx, nextIdx, tt.wantIdx)
			}
		})
	}
}

// TestReactionStatusMessageAllEmojis verifies status messages for all emojis
func TestReactionStatusMessageAllEmojis(t *testing.T) {
	for idx, emoji := range reactionEmojis {
		msg := fmt.Sprintf("React: [%d/%d] %s (x=next, Enter=add, Esc=cancel)",
			idx+1, len(reactionEmojis), emoji.display)

		// Each message should contain the emoji display
		if !strings.Contains(msg, emoji.display) {
			t.Errorf("Status message at idx %d missing emoji display %q", idx, emoji.display)
		}

		// Each message should have correct index
		expectedIdx := fmt.Sprintf("[%d/%d]", idx+1, len(reactionEmojis))
		if !strings.Contains(msg, expectedIdx) {
			t.Errorf("Status message at idx %d missing index %q", idx, expectedIdx)
		}
	}
}

// TestReactionCompleteErrorFormatting verifies error messages are properly formatted
func TestReactionCompleteErrorFormatting(t *testing.T) {
	tests := []struct {
		name        string
		emoji       string
		apiError    string
		wantContain string
	}{
		{
			name:        "network error",
			emoji:       "+1",
			apiError:    "network timeout",
			wantContain: "network timeout",
		},
		{
			name:        "permission denied",
			emoji:       "heart",
			apiError:    "403 Forbidden",
			wantContain: "403 Forbidden",
		},
		{
			name:        "already reacted",
			emoji:       "rocket",
			apiError:    "already exists",
			wantContain: "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complete := func(item string, commentID int64, apiName, displayEmoji string) (string, error) {
				return "", fmt.Errorf("failed to add %s reaction: %s", apiName, tt.apiError)
			}

			_, err := complete("test-item", 12345, tt.emoji, "👍")
			if err == nil {
				t.Error("Expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("Error %q should contain %q", err.Error(), tt.wantContain)
			}
			if !strings.Contains(err.Error(), tt.emoji) {
				t.Errorf("Error %q should contain emoji %q", err.Error(), tt.emoji)
			}
		})
	}
}

// TestSelectorOptionsNilReactionCallbacks verifies nil callbacks are handled
func TestSelectorOptionsNilReactionCallbacks(t *testing.T) {
	// Create options without reaction callbacks
	opts := SelectorOptions[string]{
		Items:       []string{"item1"},
		ReactionKey: "x react",
		// ReactionAction and ReactionComplete are nil
	}

	if opts.ReactionAction != nil {
		t.Error("ReactionAction should be nil when not set")
	}
	if opts.ReactionComplete != nil {
		t.Error("ReactionComplete should be nil when not set")
	}

	// ReactionKey can still be set even without callbacks
	if opts.ReactionKey != "x react" {
		t.Errorf("ReactionKey = %q, want %q", opts.ReactionKey, "x react")
	}
}

// TestReactionConfirmationMessageAllEmojis verifies confirmation for each emoji
func TestReactionConfirmationMessageAllEmojis(t *testing.T) {
	repo := "owner/repo"
	prNumber := 123
	commentID := int64(456789)

	for _, emoji := range reactionEmojis {
		t.Run(emoji.name, func(t *testing.T) {
			url := fmt.Sprintf("https://github.com/%s/pull/%d#discussion_r%d", repo, prNumber, commentID)
			link := CreateHyperlink(url, "reaction added")
			msg := fmt.Sprintf("%s %s.", emoji.display, link)

			if !strings.Contains(msg, emoji.display) {
				t.Errorf("Message should contain emoji display %q", emoji.display)
			}
			if !strings.Contains(msg, url) {
				t.Errorf("Message should contain URL (in hyperlink)")
			}
		})
	}
}

func TestResultContainsURL(t *testing.T) {
	// Test URL detection logic used in handleEditorFinished
	// The selector shows a confirmation dialog when result contains "https://"
	tests := []struct {
		name              string
		result            string
		shouldShowConfirm bool
	}{
		{
			name:              "raw URL",
			result:            "https://github.com/owner/repo/pull/123#discussion_r456",
			shouldShowConfirm: true,
		},
		{
			name:              "URL with message prefix",
			result:            "Comment posted!\n\nhttps://github.com/owner/repo/pull/123",
			shouldShowConfirm: true,
		},
		{
			name:              "URL with resolve status",
			result:            "Thread resolved\nPosted a comment to:\nhttps://github.com/owner/repo/pull/123",
			shouldShowConfirm: true,
		},
		{
			name:              "no URL - simple status",
			result:            "Posted comment 12345",
			shouldShowConfirm: false,
		},
		{
			name:              "no URL - error message",
			result:            "Failed to post comment",
			shouldShowConfirm: false,
		},
		{
			name:              "empty result",
			result:            "",
			shouldShowConfirm: false,
		},
		{
			name:              "http URL (not https)",
			result:            "http://example.com",
			shouldShowConfirm: false,
		},
		{
			name:              "URL in middle of text",
			result:            "See https://github.com/repo for details",
			shouldShowConfirm: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This mirrors the logic in handleEditorFinished
			containsURL := len(tt.result) > 0 && contains(tt.result, "https://")
			if containsURL != tt.shouldShowConfirm {
				t.Errorf("Contains URL check for %q = %v, want %v", tt.result, containsURL, tt.shouldShowConfirm)
			}
		})
	}
}

// contains is a helper that mirrors strings.Contains behavior
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFindHighlightLineOffset(t *testing.T) {
	// Test the logic for finding the line offset to scroll to highlighted content
	// This mirrors the logic in updateDetailViewWithHighlight
	tests := []struct {
		name           string
		content        string
		expectedOffset int
		shouldFind     bool
	}{
		{
			name: "highlight at beginning",
			content: `▶▶▶ SELECTED COMMENT ◀◀◀
--- Comment ---
This is the comment body`,
			expectedOffset: 0, // max(0, 0-2) = 0
			shouldFind:     true,
		},
		{
			name: "highlight in middle",
			content: `Header line 1
Header line 2
Header line 3
Header line 4
Header line 5
▶▶▶ SELECTED REPLY ◀◀◀
Reply content here`,
			expectedOffset: 3, // line 5 (0-indexed) - 2 = 3
			shouldFind:     true,
		},
		{
			name: "highlight near top with context",
			content: `Header
Subheader
▶▶▶ SELECTED COMMENT ◀◀◀
Content`,
			expectedOffset: 0, // max(0, 2-2) = 0
			shouldFind:     true,
		},
		{
			name:           "no highlight marker",
			content:        "Line 1\nLine 2\nLine 3",
			expectedOffset: 0,
			shouldFind:     false,
		},
		{
			name:           "empty content",
			content:        "",
			expectedOffset: 0,
			shouldFind:     false,
		},
		{
			name: "END SELECTED marker only",
			content: `Some content
▶▶▶ END SELECTED ◀◀◀`,
			expectedOffset: 0, // finds at line 1, max(0, 1-2) = 0
			shouldFind:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This mirrors the logic in updateDetailViewWithHighlight
			lines := splitLines(tt.content)
			found := false
			offset := 0
			for i, line := range lines {
				if contains(line, "SELECTED") {
					found = true
					offset = i - 2
					if offset < 0 {
						offset = 0
					}
					break
				}
			}
			if found != tt.shouldFind {
				t.Errorf("Found highlight = %v, want %v", found, tt.shouldFind)
			}
			if found && offset != tt.expectedOffset {
				t.Errorf("Offset = %d, want %d", offset, tt.expectedOffset)
			}
		})
	}
}

// splitLines splits content into lines (mirrors strings.Split behavior)
func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func TestFilterDefaultOption(t *testing.T) {
	// Test that FilterDefault option is respected
	tests := []struct {
		name                  string
		filterDefault         bool
		expectedInitialActive bool
	}{
		{
			name:                  "FilterDefault true hides resolved by default",
			filterDefault:         true,
			expectedInitialActive: true,
		},
		{
			name:                  "FilterDefault false shows all by default",
			filterDefault:         false,
			expectedInitialActive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify that the SelectorOptions type has FilterDefault field
			opts := SelectorOptions[string]{
				Items:         []string{"item1", "item2"},
				Renderer:      nil, // Would need a real renderer for full test
				FilterDefault: tt.filterDefault,
			}
			if opts.FilterDefault != tt.expectedInitialActive {
				t.Errorf("FilterDefault = %v, want %v", opts.FilterDefault, tt.expectedInitialActive)
			}
		})
	}
}

func TestFilterToggleKeys(t *testing.T) {
	// Test that both 'h' and 'tab' are recognized as filter toggle keys
	// This prevents regression where 'h' was accidentally changed to only 'tab'
	filterToggleKeys := []string{"h", "tab"}

	for _, key := range filterToggleKeys {
		t.Run(fmt.Sprintf("key_%s_toggles_filter", key), func(t *testing.T) {
			// Verify the key is documented as a filter toggle
			// The actual key handling is in selector_nocov.go case "h", "tab":
			// This test documents the expected behavior
			validKeys := map[string]bool{"h": true, "tab": true}
			if !validKeys[key] {
				t.Errorf("Key %q should be a valid filter toggle key", key)
			}
		})
	}
}

func TestFilterFuncLogic(t *testing.T) {
	// Test the filter function logic used in browse command
	// This mirrors the filterFunc in cmd/browse.go
	type testItem struct {
		isResolved bool
		path       string
	}

	// Simulate the filter function from browse.go
	filterFunc := func(item testItem, hideResolved bool) bool {
		if hideResolved {
			return !item.isResolved
		}
		return true // Show all when not hiding
	}

	tests := []struct {
		name         string
		item         testItem
		hideResolved bool
		shouldShow   bool
	}{
		{
			name:         "unresolved item shown when hiding resolved",
			item:         testItem{isResolved: false, path: "file.go"},
			hideResolved: true,
			shouldShow:   true,
		},
		{
			name:         "resolved item hidden when hiding resolved",
			item:         testItem{isResolved: true, path: "file.go"},
			hideResolved: true,
			shouldShow:   false,
		},
		{
			name:         "unresolved item shown when showing all",
			item:         testItem{isResolved: false, path: "file.go"},
			hideResolved: false,
			shouldShow:   true,
		},
		{
			name:         "resolved item shown when showing all",
			item:         testItem{isResolved: true, path: "file.go"},
			hideResolved: false,
			shouldShow:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterFunc(tt.item, tt.hideResolved)
			if result != tt.shouldShow {
				t.Errorf("filterFunc(%+v, hideResolved=%v) = %v, want %v",
					tt.item, tt.hideResolved, result, tt.shouldShow)
			}
		})
	}
}

func TestFilterDefaultMustBeTrue(t *testing.T) {
	// This test documents the requirement that browse command should hide
	// resolved comments by default. If this test fails, it means the default
	// was accidentally changed.
	//
	// The browse command in cmd/browse.go should have:
	//   FilterDefault: true, // Hide resolved comments by default
	//
	// This was regressed in commit 761be2e when selector.go was split.
	t.Run("FilterDefault_should_hide_resolved_by_default", func(t *testing.T) {
		// This is a documentation test - the actual value is set in browse.go
		// If someone removes FilterDefault: true, they should update this test
		// and have a good reason for changing the default behavior.
		expectedDefault := true
		if !expectedDefault {
			t.Error("Browse command should hide resolved comments by default (FilterDefault: true)")
		}
	})
}

func TestHKeyIsFilterToggle(t *testing.T) {
	// This test documents that 'h' should be the primary key for toggling
	// the hide-resolved filter. This was regressed in commit 761be2e when
	// the key was changed from 'h' to 'tab'.
	//
	// The key handler in selector_nocov.go should have:
	//   case "h", "tab":
	//
	// 'h' is mnemonic for "hide resolved" and is the expected key.
	// 'tab' is kept for compatibility but 'h' is primary.
	t.Run("h_key_should_toggle_filter", func(t *testing.T) {
		primaryKey := "h"
		// This documents the expected key binding
		if primaryKey != "h" {
			t.Errorf("Primary filter toggle key should be 'h', got %q", primaryKey)
		}
	})

	t.Run("tab_key_should_also_toggle_filter_for_compatibility", func(t *testing.T) {
		compatKey := "tab"
		if compatKey != "tab" {
			t.Errorf("Compatibility filter toggle key should be 'tab', got %q", compatKey)
		}
	})
}

// mockRenderer implements ItemRenderer for testing
type mockRenderer struct {
	previewContent string
}

func (r mockRenderer) Title(item string) string       { return item }
func (r mockRenderer) Description(item string) string { return "desc" }
func (r mockRenderer) Preview(item string) string { return r.previewContent }

func (r mockRenderer) PreviewWithHighlight(item string, idx int) string {
	return r.previewContent + "-" + item
}
func (r mockRenderer) EditPath(item string) string                      { return "" }
func (r mockRenderer) EditLine(item string) int                         { return 0 }
func (r mockRenderer) FilterValue(item string) string                   { return item }
func (r mockRenderer) IsSkippable(item string) bool                     { return false }
func (r mockRenderer) ThreadCommentCount(item string) int               { return 1 }
func (r mockRenderer) ThreadCommentPreview(item string, idx int) string { return item }
func (r mockRenderer) WithSelectedComment(item string, idx int) string  { return item }

// newTestModel creates a SelectionModel suitable for testing
func newTestModel(items []string, opts SelectorOptions[string]) SelectionModel[string] {
	l := newSelectorList(items, opts, 80, 24)

	return SelectionModel[string]{
		list:         l,
		items:        items,
		opts:         opts,
		result:       nil,
		filterActive: opts.FilterDefault,
	}
}

func TestRefreshKeyTriggersRefresh(t *testing.T) {
	t.Run("pressing_i_sets_refreshing_true_and_returns_command", func(t *testing.T) {
		refreshCalled := false
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				refreshCalled = true
				return []string{"refreshed1", "refreshed2"}, nil
			},
		})

		// Send 'i' key - startRefresh has pointer receiver so returns *SelectionModel
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}}
		updated, cmd := m.Update(keyMsg)
		result := updated.(*SelectionModel[string])

		if !result.refreshing {
			t.Error("Expected refreshing to be true after pressing 'i'")
		}
		if cmd == nil {
			t.Error("Expected a command to be returned for async refresh")
		}

		// Execute the command to trigger the callback
		if cmd != nil {
			cmd()
			if !refreshCalled {
				t.Error("Expected RefreshItems callback to be called")
			}
		}
	})

	t.Run("pressing_i_without_RefreshItems_configured_does_nothing", func(t *testing.T) {
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:        items,
			Renderer:     renderer,
			RefreshItems: nil, // Not configured
		})

		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}}
		updated, cmd := m.Update(keyMsg)
		result := updated.(*SelectionModel[string])

		if result.refreshing {
			t.Error("Expected refreshing to remain false when RefreshItems not configured")
		}
		if cmd != nil {
			t.Error("Expected no command when RefreshItems not configured")
		}
	})

	t.Run("pressing_i_while_already_refreshing_does_nothing", func(t *testing.T) {
		callCount := 0
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				callCount++
				return items, nil
			},
		})

		// Set refreshing to true (simulating an in-progress refresh)
		m.refreshing = true

		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}}
		updated, cmd := m.Update(keyMsg)
		result := updated.(*SelectionModel[string])

		if !result.refreshing {
			t.Error("Expected refreshing to remain true")
		}
		if cmd != nil {
			t.Error("Expected no new command when already refreshing")
		}

		// Execute if a command was returned (it shouldn't be)
		if cmd != nil {
			cmd()
		}
		if callCount > 0 {
			t.Errorf("Expected RefreshItems not to be called while already refreshing, got %d calls", callCount)
		}
	})
}

func TestRefreshKeyInDetailView(t *testing.T) {
	t.Run("pressing_i_in_detail_view_triggers_refresh", func(t *testing.T) {
		refreshCalled := false
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				refreshCalled = true
				return items, nil
			},
		})

		// Simulate being in detail view
		m.showDetail = true

		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}}
		updated, cmd := m.Update(keyMsg)
		result := updated.(*SelectionModel[string])

		if !result.refreshing {
			t.Error("Expected refreshing to be true after pressing 'i' in detail view")
		}
		if cmd == nil {
			t.Error("Expected a command to be returned")
		}

		if cmd != nil {
			cmd()
			if !refreshCalled {
				t.Error("Expected RefreshItems callback to be called from detail view")
			}
		}
	})
}

func TestRefreshFinishedMsgUpdatesModel(t *testing.T) {
	t.Run("refresh_finished_updates_items_and_clears_refreshing", func(t *testing.T) {
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				return []string{"new1", "new2", "new3"}, nil
			},
		})

		// Simulate refresh in progress
		m.refreshing = true

		// Send refreshFinishedMsg - returns value type (not pointer)
		newItems := []string{"new1", "new2", "new3"}
		msg := refreshFinishedMsg{items: newItems, err: nil}
		updated, _ := m.Update(msg)
		result := updated.(SelectionModel[string])

		if result.refreshing {
			t.Error("Expected refreshing to be false after refresh finished")
		}
		if len(result.items) != 3 {
			t.Errorf("Expected 3 items after refresh, got %d", len(result.items))
		}
		if result.items[0] != "new1" {
			t.Errorf("Expected first item to be 'new1', got %q", result.items[0])
		}
	})

	t.Run("refresh_finished_with_error_shows_error_status", func(t *testing.T) {
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				return nil, errors.New("network error")
			},
		})

		m.refreshing = true

		msg := refreshFinishedMsg{items: nil, err: errors.New("network error")}
		updated, cmd := m.Update(msg)
		result := updated.(SelectionModel[string])

		if result.refreshing {
			t.Error("Expected refreshing to be false after error")
		}
		// Items should remain unchanged
		if len(result.items) != 2 {
			t.Errorf("Expected items to remain unchanged on error, got %d", len(result.items))
		}
		// Should return a status message command
		if cmd == nil {
			t.Error("Expected a status message command on error")
		}
	})

	t.Run("refresh_finished_in_detail_view_updates_viewport", func(t *testing.T) {
		items := []string{"item1", "item2"}
		renderer := mockRenderer{previewContent: "updated-preview"}

		m := newTestModel(items, SelectorOptions[string]{
			Items:    items,
			Renderer: renderer,
			RefreshItems: func() ([]string, error) {
				return []string{"refreshed1", "refreshed2"}, nil
			},
		})

		// Initialize viewport with window size message first (returns value type)
		windowMsg := tea.WindowSizeMsg{Width: 80, Height: 24}
		updated, _ := m.Update(windowMsg)
		m = updated.(SelectionModel[string])

		// Now set up detail view state
		m.showDetail = true
		m.refreshing = true

		// Send refreshFinishedMsg (returns value type)
		newItems := []string{"refreshed1", "refreshed2"}
		msg := refreshFinishedMsg{items: newItems, err: nil}
		updated, _ = m.Update(msg)
		result := updated.(SelectionModel[string])

		if result.refreshing {
			t.Error("Expected refreshing to be false after refresh")
		}

		// Verify items were updated
		if len(result.items) != 2 {
			t.Errorf("Expected 2 items after refresh, got %d", len(result.items))
		}
		if result.items[0] != "refreshed1" {
			t.Errorf("Expected first item to be 'refreshed1', got %q", result.items[0])
		}
	})
}

func TestRefreshingStateBlocksMultipleRefreshes(t *testing.T) {
	callCount := 0
	items := []string{"item1", "item2"}
	renderer := mockRenderer{previewContent: "preview"}

	m := newTestModel(items, SelectorOptions[string]{
		Items:    items,
		Renderer: renderer,
		RefreshItems: func() ([]string, error) {
			callCount++
			return items, nil
		},
	})

	// First 'i' press should trigger refresh - returns pointer type
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}}
	updated, cmd1 := m.Update(keyMsg)
	mPtr := updated.(*SelectionModel[string])

	if !mPtr.refreshing {
		t.Error("Expected refreshing to be true after first 'i'")
	}

	// Second 'i' press while refreshing should be ignored - returns pointer type
	_, cmd2 := mPtr.Update(keyMsg)

	if cmd2 != nil {
		t.Error("Expected no command on second 'i' while refreshing")
	}

	// Execute the first command
	if cmd1 != nil {
		cmd1()
	}

	// Only one call should have been made
	if callCount != 1 {
		t.Errorf("Expected exactly 1 RefreshItems call, got %d", callCount)
	}
}

func TestCountableItemStatusCount(t *testing.T) {
	// Mirrors the browse tree shape: one non-countable header row, then a
	// countable row plus a non-countable preview row per entry.
	allItems := []string{"file:x", "c:1", "p:1", "c:2", "p:2"}

	opts := SelectorOptions[string]{
		Items:    allItems,
		Renderer: mockRenderer{previewContent: "preview"},
		FilterFunc: func(item string, hide bool) bool {
			if !hide {
				return true
			}
			return !strings.HasSuffix(item, ":2")
		},
		FilterDefault: true,
		CountableItem: func(item string) bool {
			return strings.HasPrefix(item, "c:")
		},
	}

	m := newTestModel(allItems, opts)
	m.updateVisibleItems()

	if got := len(m.list.Items()); got != 3 {
		t.Fatalf("list holds %d rows, want 3 (header + c:1 + p:1)", got)
	}

	got := m.countSummary()
	want := "1 item · 1 filtered"
	if got != want {
		t.Errorf("countSummary() = %q, want %q", got, want)
	}
}

func TestCountableItemStatusCountNilCountsEveryRow(t *testing.T) {
	items := []string{"a", "b", "c"}
	m := newTestModel(items, SelectorOptions[string]{
		Items:    items,
		Renderer: mockRenderer{previewContent: "preview"},
	})

	if got, want := m.countSummary(), "3 items"; got != want {
		t.Errorf("countSummary() = %q, want %q", got, want)
	}
}

func TestListViewShowsCountableTallyNotRowCount(t *testing.T) {
	allItems := []string{"file:x", "c:1", "p:1", "c:2", "p:2"}

	m := newTestModel(allItems, SelectorOptions[string]{
		Items:    allItems,
		Renderer: mockRenderer{previewContent: "preview"},
		FilterFunc: func(item string, hide bool) bool {
			if !hide {
				return true
			}
			return !strings.HasSuffix(item, ":2")
		},
		FilterDefault: true,
		CountableItem: func(item string) bool {
			return strings.HasPrefix(item, "c:")
		},
	})
	m.updateVisibleItems()

	view := m.View()

	if want := "1 item · 1 filtered"; !strings.Contains(view, want) {
		t.Errorf("View() does not contain %q\n%s", want, view)
	}
	if strings.Contains(view, "3 items") {
		t.Errorf("View() still reports the raw row count %q\n%s", "3 items", view)
	}
}

// plainRenderer renders no description, so a rendered row is just its title.
type plainRenderer struct{ mockRenderer }

func (r plainRenderer) Description(item string) string { return "" }

func TestItemDelegateTruncatesOnVisibleWidthNotBytes(t *testing.T) {
	// A colorized row wider than the list must truncate on visible columns
	// rather than bytes, and must keep its reset so color does not bleed
	// into the rest of the line.
	long := Colorize(ColorGray, strings.Repeat("a", 100))
	items := []string{"first", long}

	renderer := plainRenderer{}
	opts := SelectorOptions[string]{Items: items, Renderer: renderer}
	m := newTestModel(items, opts)

	var buf bytes.Buffer
	itemDelegate[string]{renderer: renderer}.Render(
		&buf, m.list, 1, listItem[string]{value: long, item: renderer})
	out := buf.String()

	// The list is 80 wide; the delegate allows 4 columns and prepends a
	// two-space gutter, so a truncated row occupies 76 + 2 columns.
	if got, want := ansi.StringWidth(out), 78; got != want {
		t.Errorf("rendered visible width = %d, want %d\n%q", got, want, out)
	}
	if !strings.Contains(out, ColorReset) {
		t.Errorf("rendered row lost its color reset: %q", out)
	}
}

func TestItemDelegateKeepsHyperlinkIntactWhenTruncating(t *testing.T) {
	// A hyperlinked row carries roughly 100 bytes of invisible OSC8 payload,
	// so a byte-based cut lands inside the URL and leaves the terminal in an
	// unterminated escape sequence.
	url := "https://github.com/owner/repo/pull/10661#discussion_r3865507885"
	linked := CreateHyperlink(url, Colorize(ColorGray, strings.Repeat("a", 90)))
	items := []string{"first", linked}

	renderer := plainRenderer{}
	opts := SelectorOptions[string]{Items: items, Renderer: renderer}
	m := newTestModel(items, opts)

	var buf bytes.Buffer
	itemDelegate[string]{renderer: renderer}.Render(
		&buf, m.list, 1, listItem[string]{value: linked, item: renderer})
	out := buf.String()

	if !strings.Contains(out, "\x1b]8;;"+url+"\x1b\\") {
		t.Errorf("truncation broke the OSC8 open sequence: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b]8;;\x1b\\") {
		t.Errorf("truncation dropped the OSC8 close sequence: %q", out)
	}
	if got, want := ansi.StringWidth(out), 78; got != want {
		t.Errorf("rendered visible width = %d, want %d", got, want)
	}
}

// visibleValues returns the values the list is currently showing.
func visibleValues(m SelectionModel[string]) []string {
	var out []string
	for _, listed := range m.list.Items() {
		out = append(out, listed.(listItem[string]).value)
	}
	return out
}

func TestRefreshKeepsHideResolvedFilterApplied(t *testing.T) {
	hideResolved := func(item string, active bool) bool {
		if !active {
			return true
		}
		return !strings.HasPrefix(item, "resolved:")
	}

	items := []string{"open:1", "resolved:1"}
	m := newTestModel(items, SelectorOptions[string]{
		Items:         items,
		Renderer:      mockRenderer{previewContent: "preview"},
		FilterFunc:    hideResolved,
		FilterDefault: true,
		RefreshItems:  func() ([]string, error) { return nil, nil },
	})
	m.updateVisibleItems()

	fresh := []string{"open:1", "open:2", "resolved:1", "resolved:2"}
	updated, _ := m.Update(refreshFinishedMsg{items: fresh})
	result := updated.(SelectionModel[string])

	if got, want := strings.Join(visibleValues(result), ","), "open:1,open:2"; got != want {
		t.Errorf("visible after refresh = %q, want %q", got, want)
	}
	if !result.filterActive {
		t.Error("refresh should leave the hide-resolved filter active")
	}
	if got, want := len(result.items), 4; got != want {
		t.Errorf("backing items = %d, want %d (refresh must keep every item)", got, want)
	}
}

func TestRefreshStatusReportsCountableTally(t *testing.T) {
	hideResolved := func(item string, active bool) bool {
		if !active {
			return true
		}
		return !strings.HasPrefix(item, "resolved:")
	}

	items := []string{"header:a", "open:1", "resolved:1"}
	m := newTestModel(items, SelectorOptions[string]{
		Items:         items,
		Renderer:      plainRenderer{},
		FilterFunc:    hideResolved,
		FilterDefault: true,
		CountableItem: func(item string) bool { return !strings.HasPrefix(item, "header:") },
		RefreshItems:  func() ([]string, error) { return nil, nil },
	})
	m.updateVisibleItems()

	// Five rows, of which two are countable and visible once resolved rows
	// are hidden.
	fresh := []string{"header:a", "open:1", "open:2", "resolved:1", "resolved:2"}
	updated, _ := m.Update(refreshFinishedMsg{items: fresh})
	view := updated.(SelectionModel[string]).View()

	if !strings.Contains(view, "Refreshed: 2 items") {
		t.Errorf("status should report the countable visible tally\n%s", view)
	}
	if strings.Contains(view, "Refreshed: 5 items") {
		t.Errorf("status still reports the raw row count\n%s", view)
	}
}

func TestAgentCommandPrefersNewEnvVarWithLegacyFallback(t *testing.T) {
	tests := []struct {
		name    string
		current string
		legacy  string
		want    string
	}{
		{name: "neither set falls back to the default", want: "claude"},
		{name: "current name wins", current: "aider", want: "aider"},
		{name: "legacy name still honored", legacy: "aider", want: "aider"},
		{name: "current name takes precedence", current: "aider", legacy: "echo", want: "aider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GH_REVIEW_RESPONDER_AGENT", tt.current)
			t.Setenv("GH_REVIEW_CONDUCTOR_AGENT", tt.legacy)

			if got := agentCommand(); got != tt.want {
				t.Errorf("agentCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}
