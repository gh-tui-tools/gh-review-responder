package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gh-tui-tools/gh-review-responder/pkg/applier"
	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
	"github.com/gh-tui-tools/gh-review-responder/pkg/ui"
)

func TestBrowseItemRenderer_IsSkippable(t *testing.T) {
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}

	tests := []struct {
		name     string
		item     BrowseItem
		expected bool
	}{
		{
			name: "file header is not skippable",
			item: BrowseItem{
				Type: "file",
				Path: "src/main.go",
			},
			expected: false,
		},
		{
			name: "comment is not skippable",
			item: BrowseItem{
				Type: "comment",
				Path: "src/main.go",
				Comment: &github.ReviewComment{
					ID:     123,
					Author: "reviewer",
					Body:   "Consider refactoring this",
				},
			},
			expected: false,
		},
		{
			name: "preview is not skippable (prevents strikethrough styling)",
			item: BrowseItem{
				Type:      "comment_preview",
				Path:      "src/main.go",
				IsPreview: true,
				Comment: &github.ReviewComment{
					ID:     123,
					Author: "reviewer",
					Body:   "Consider refactoring this",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderer.IsSkippable(tt.item)
			if result != tt.expected {
				t.Errorf("IsSkippable(%v) = %v, want %v", tt.item.Type, result, tt.expected)
			}
		})
	}
}

func TestBrowseItemRenderer_Title_PreviewUsesGrayColor(t *testing.T) {
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}

	item := BrowseItem{
		Type:      "comment_preview",
		Path:      "src/main.go",
		IsPreview: true,
		Comment: &github.ReviewComment{
			ID:     123,
			Author: "reviewer",
			Body:   "Consider refactoring this function for better readability",
		},
	}

	title := renderer.Title(item)

	// Title should contain the preview text
	if !strings.Contains(title, "Consider refactoring") {
		t.Errorf("Title should contain preview text, got: %q", title)
	}

	// Title should use gray color (ANSI code 90) when colors are enabled
	if ui.ColorsEnabled() {
		if !strings.Contains(title, ui.ColorGray) {
			t.Errorf("Title should use gray color code, got: %q", title)
		}
		if !strings.Contains(title, ui.ColorReset) {
			t.Errorf("Title should include color reset code, got: %q", title)
		}
	}

	// Title should be indented
	if !strings.HasPrefix(title, "      ") {
		t.Errorf("Title should be indented with 6 spaces, got: %q", title)
	}
}

func TestBrowseItemRenderer_Title_PreviewTruncation(t *testing.T) {
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}

	tests := []struct {
		name           string
		body           string
		shouldTruncate bool
		shouldHaveEllipsis bool
	}{
		{
			name:           "short single line",
			body:           "Short comment",
			shouldTruncate: false,
			shouldHaveEllipsis: false,
		},
		{
			name:           "multi-line adds ellipsis",
			body:           "First line\nSecond line",
			shouldTruncate: false,
			shouldHaveEllipsis: true,
		},
		{
			name:           "very long line gets truncated",
			body:           strings.Repeat("a", 100),
			shouldTruncate: true,
			shouldHaveEllipsis: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := BrowseItem{
				Type:      "comment_preview",
				Path:      "src/main.go",
				IsPreview: true,
				Comment: &github.ReviewComment{
					ID:     123,
					Author: "reviewer",
					Body:   tt.body,
				},
			}

			title := renderer.Title(item)

			if tt.shouldTruncate {
				// Very long lines should be truncated to ~80 chars
				// Account for indentation and color codes
				plainText := strings.TrimPrefix(title, "      ")
				// Remove ANSI codes for length check
				var ansiRegex = regexp.MustCompile("\x1b\\[[0-9;]*m")
				plainText = ansiRegex.ReplaceAllString(plainText, "")
				if len(plainText) > 80 {
					t.Errorf("Title should be truncated, got length %d: %q", len(plainText), plainText)
				}
			}

			if tt.shouldHaveEllipsis {
				if !strings.Contains(title, "...") {
					t.Errorf("Title should contain ellipsis, got: %q", title)
				}
			}
		})
	}
}

func TestBuildCommentTree(t *testing.T) {
	comments := []*github.ReviewComment{
		{
			ID:     1,
			Path:   "file1.go",
			Line:   10,
			Author: "user1",
			Body:   "Comment on file1",
		},
		{
			ID:     2,
			Path:   "file2.go",
			Line:   20,
			Author: "user2",
			Body:   "Comment on file2",
		},
		{
			ID:     3,
			Path:   "file1.go",
			Line:   5,
			Author: "user3",
			Body:   "Earlier comment on file1",
		},
	}

	items := buildCommentTree(comments)

	// Should have: file1 header + 2 comments + 2 previews + file2 header + 1 comment + 1 preview
	// = 2 file headers + 3 comments + 3 previews = 8 items
	expectedCount := 8
	if len(items) != expectedCount {
		t.Errorf("buildCommentTree returned %d items, want %d", len(items), expectedCount)
	}

	// First item should be file1.go header (alphabetically first)
	if items[0].Type != "file" || items[0].Path != "file1.go" {
		t.Errorf("First item should be file1.go header, got type=%q path=%q", items[0].Type, items[0].Path)
	}

	// Comments within a file should be sorted by line number
	// file1.go has comments at lines 5 and 10, so line 5 should come first
	var file1Comments []BrowseItem
	for _, item := range items {
		if item.Path == "file1.go" && item.Type == "comment" {
			file1Comments = append(file1Comments, item)
		}
	}

	if len(file1Comments) != 2 {
		t.Fatalf("Expected 2 comments for file1.go, got %d", len(file1Comments))
	}

	if file1Comments[0].Comment.Line != 5 {
		t.Errorf("First comment in file1.go should be at line 5, got %d", file1Comments[0].Comment.Line)
	}

	if file1Comments[1].Comment.Line != 10 {
		t.Errorf("Second comment in file1.go should be at line 10, got %d", file1Comments[1].Comment.Line)
	}

	// Each comment should be followed by a preview
	for i, item := range items {
		if item.Type == "comment" && i+1 < len(items) {
			next := items[i+1]
			if next.Type != "comment_preview" {
				t.Errorf("Comment at index %d should be followed by preview, got %q", i, next.Type)
			}
			if next.Comment.ID != item.Comment.ID {
				t.Errorf("Preview should reference same comment, got IDs %d vs %d", next.Comment.ID, item.Comment.ID)
			}
		}
	}
}

func TestBrowseItemRenderer_Title_ReplyCount(t *testing.T) {
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}

	tests := []struct {
		name           string
		replyCount     int
		wantContains   string
		wantNotContain string
	}{
		{
			name:           "no replies shows no count",
			replyCount:     0,
			wantContains:   "",
			wantNotContain: "repl",
		},
		{
			name:         "one reply shows singular",
			replyCount:   1,
			wantContains: "[1 reply]",
		},
		{
			name:         "multiple replies shows plural",
			replyCount:   3,
			wantContains: "[3 replies]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var threadComments []github.ThreadComment
			for i := 0; i < tt.replyCount; i++ {
				threadComments = append(threadComments, github.ThreadComment{
					ID:     int64(100 + i),
					Author: "replier",
					Body:   "Reply body",
				})
			}

			item := BrowseItem{
				Type: "comment",
				Path: "src/main.go",
				Comment: &github.ReviewComment{
					ID:             123,
					Author:         "reviewer",
					Body:           "Original comment",
					Line:           42,
					ThreadComments: threadComments,
				},
			}

			title := renderer.Title(item)

			if tt.wantContains != "" && !strings.Contains(title, tt.wantContains) {
				t.Errorf("Title should contain %q, got: %q", tt.wantContains, title)
			}
			if tt.wantNotContain != "" && strings.Contains(title, tt.wantNotContain) {
				t.Errorf("Title should not contain %q, got: %q", tt.wantNotContain, title)
			}
		})
	}
}

func TestStripMarkdownForPreview(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text unchanged",
			input:    "This is plain text",
			expected: "This is plain text",
		},
		{
			name:     "removes image markdown",
			input:    "Text before ![alt text](https://example.com/image.png) text after",
			expected: "Text before  text after",
		},
		{
			name:     "converts link to text",
			input:    "Check out [this link](https://example.com) for more",
			expected: "Check out this link for more",
		},
		{
			name:     "handles multiple images and links",
			input:    "![img1](url1) and [link1](url2) and ![img2](url3)",
			expected: "and link1 and",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripMarkdownForPreview(tt.input)
			if result != tt.expected {
				t.Errorf("stripMarkdownForPreview(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPreviewWithHighlight_SuggestionDiff(t *testing.T) {
	// Create a temp file so PreviewSuggestion can read it
	dir := t.TempDir()
	// Resolve symlinks so paths match os.Getwd() on macOS (/var -> /private/var)
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "test.go")
	fileContent := "package main\n\nfunc hello() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// chdir so validatePath accepts the file path
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	app := applier.New()

	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
		applier:        app,
	}

	item := BrowseItem{
		Type: "comment",
		Path: filePath,
		Comment: &github.ReviewComment{
			ID:            1,
			Author:        "reviewer",
			Body:          "Use Println from log package",
			Path:          filePath,
			Line:          4,
			StartLine:     0,
			HasSuggestion: true,
			SuggestedCode: "\tlog.Println(\"hello\")\n",
		},
	}

	preview := renderer.PreviewWithHighlight(item, -1)

	// Should show "Suggestion Diff" header (not "Suggested Code")
	if !strings.Contains(preview, "Suggestion Diff") {
		t.Errorf("preview should contain \"Suggestion Diff\" header, got:\n%s", preview)
	}

	// Should contain actual diff markers
	if !strings.Contains(preview, "-\tfmt.Println") {
		t.Errorf("preview should show removed line with -, got:\n%s", preview)
	}
	if !strings.Contains(preview, "+\tlog.Println") {
		t.Errorf("preview should show added line with +, got:\n%s", preview)
	}
}

func TestPreviewWithHighlight_SuggestionDiffFallback(t *testing.T) {
	// When file doesn\'t exist, should fall back to "Suggested Code"
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
		applier:        applier.New(),
	}

	item := BrowseItem{
		Type: "comment",
		Path: "/nonexistent/file.go",
		Comment: &github.ReviewComment{
			ID:            1,
			Author:        "reviewer",
			Body:          "Fix this",
			Path:          "/nonexistent/file.go",
			Line:          1,
			StartLine:     0,
			HasSuggestion: true,
			SuggestedCode: "replacement code\n",
		},
	}

	preview := renderer.PreviewWithHighlight(item, -1)

	// Should fall back to "Suggested Code" header
	if !strings.Contains(preview, "Suggested Code") {
		t.Errorf("preview should fall back to \"Suggested Code\" when file missing, got:\n%s", preview)
	}
}

func TestPreviewWithHighlight_NoApplier(t *testing.T) {
	// When no applier is set, should show "Suggested Code"
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
		applier:        nil,
	}

	item := BrowseItem{
		Type: "comment",
		Path: "test.go",
		Comment: &github.ReviewComment{
			ID:            1,
			Author:        "reviewer",
			Body:          "Fix this",
			Path:          "test.go",
			Line:          1,
			StartLine:     0,
			HasSuggestion: true,
			SuggestedCode: "replacement\n",
		},
	}

	preview := renderer.PreviewWithHighlight(item, -1)

	if !strings.Contains(preview, "Suggested Code") {
		t.Errorf("preview should show \"Suggested Code\" without applier, got:\n%s", preview)
	}
}

func TestPreviewWithHighlight_ContextShowsTail(t *testing.T) {
	renderer := &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}

	// Create a long diff hunk (like a new file) where the context near
	// the comment is at the end
	var hunkLines []string
	hunkLines = append(hunkLines, "@@ -0,0 +1,20 @@")
	for i := 1; i <= 20; i++ {
		hunkLines = append(hunkLines, "+line "+strings.Repeat("x", i))
	}
	longHunk := strings.Join(hunkLines, "\n")

	item := BrowseItem{
		Type: "comment",
		Path: "newfile.go",
		Comment: &github.ReviewComment{
			ID:       1,
			Author:   "reviewer",
			Body:     "Comment near end of file",
			Path:     "newfile.go",
			Line:     20,
			DiffHunk: longHunk,
		},
	}

	preview := renderer.PreviewWithHighlight(item, -1)

	// Should contain "Context" header
	if !strings.Contains(preview, "Context") {
		t.Errorf("preview should contain Context section, got:\n%s", preview)
	}

	// Should show lines near the end (tail), not the beginning
	// The last line is "+line xxxxxxxxxxxxxxxxxxxx" (20 x's)
	if !strings.Contains(preview, strings.Repeat("x", 20)) {
		t.Errorf("preview context should show tail lines (near comment), got:\n%s", preview)
	}

	// Should NOT show the very first content line (line 1 with 1 x)
	// unless the hunk is short enough to show entirely
	if strings.Contains(preview, "+line x\n") {
		t.Errorf("preview context should not show lines from start of long hunk, got:\n%s", preview)
	}
}

func TestBuildCommentTreeMarksFileHeaderResolvedState(t *testing.T) {
	comments := []*github.ReviewComment{
		{ID: 1, Path: "open.go", Line: 10},
		{ID: 2, Path: "done.go", Line: 20, SubjectType: "resolved"},
		{ID: 3, Path: "mixed.go", Line: 30, SubjectType: "resolved"},
		{ID: 4, Path: "mixed.go", Line: 40},
	}

	headers := make(map[string]BrowseItem)
	for _, item := range buildCommentTree(comments) {
		if item.Type == "file" {
			headers[item.Path] = item
		}
	}

	for path, want := range map[string]bool{"open.go": true, "done.go": false, "mixed.go": true} {
		if got := headers[path].HasUnresolved; got != want {
			t.Errorf("header %s HasUnresolved = %v, want %v", path, got, want)
		}
	}
}

func TestBrowseFilterHidesFileHeadersWithNoUnresolvedComments(t *testing.T) {
	collapsed := map[string]bool{}

	openHeader := BrowseItem{Type: "file", Path: "open.go", HasUnresolved: true}
	doneHeader := BrowseItem{Type: "file", Path: "done.go"}

	if !browseFilter(openHeader, true, collapsed) {
		t.Error("header with unresolved comments should stay visible when hiding resolved")
	}
	if browseFilter(doneHeader, true, collapsed) {
		t.Error("header with only resolved comments should be hidden when hiding resolved")
	}
	if !browseFilter(doneHeader, false, collapsed) {
		t.Error("header should stay visible when resolved comments are shown")
	}
}

func TestBrowseFilterHidesCollapsedAndResolvedComments(t *testing.T) {
	resolved := &github.ReviewComment{ID: 1, Path: "a.go", SubjectType: "resolved"}
	open := &github.ReviewComment{ID: 2, Path: "a.go"}
	collapsed := map[string]bool{"b.go": true}

	if browseFilter(BrowseItem{Type: "comment", Path: "a.go", Comment: resolved}, true, collapsed) {
		t.Error("resolved comment should be hidden when hiding resolved")
	}
	if !browseFilter(BrowseItem{Type: "comment", Path: "a.go", Comment: open}, true, collapsed) {
		t.Error("unresolved comment should stay visible when hiding resolved")
	}
	if browseFilter(BrowseItem{Type: "comment_preview", Path: "b.go", Comment: open}, false, collapsed) {
		t.Error("preview row in a collapsed file should be hidden")
	}
}

func TestBrowseCountsOnlyCommentRows(t *testing.T) {
	items := buildCommentTree([]*github.ReviewComment{
		{ID: 1, Path: "a.go", Line: 10},
		{ID: 2, Path: "b.go", Line: 20},
	})

	count := 0
	for _, item := range items {
		if browseCountable(item) {
			count++
		}
	}

	if count != 2 {
		t.Errorf("browseCountable matched %d of %d rows, want 2", count, len(items))
	}
}

// osc8Re matches an OSC8 hyperlink sequence, which the CSI-only regex used
// elsewhere in this file does not cover.
var osc8Re = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\")

// stripTerminalEscapes removes both OSC8 hyperlinks and CSI color codes.
func stripTerminalEscapes(s string) string {
	s = osc8Re.ReplaceAllString(s, "")
	return regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(s, "")
}

func previewItem(body, htmlURL string) BrowseItem {
	return BrowseItem{
		Type:      "comment_preview",
		Path:      "src/main.go",
		IsPreview: true,
		Comment: &github.ReviewComment{
			ID:      456,
			Author:  "reviewer",
			Body:    body,
			HTMLURL: htmlURL,
		},
	}
}

func newPreviewRenderer() *browseItemRenderer {
	return &browseItemRenderer{
		repo:           "owner/repo",
		prNumber:       123,
		collapsedFiles: make(map[string]bool),
	}
}

func TestBrowseItemRenderer_PreviewExcerptLinksToComment(t *testing.T) {
	const url = "https://github.com/owner/repo/pull/123#discussion_r456"

	title := newPreviewRenderer().Title(previewItem("Consider using TRY() here", url))

	if !strings.Contains(title, "\x1b]8;;"+url+"\x1b\\") {
		t.Errorf("excerpt should open an OSC8 link to %s, got %q", url, title)
	}
	if !strings.Contains(title, "\x1b]8;;\x1b\\") {
		t.Errorf("excerpt should close its OSC8 link, got %q", title)
	}
	if !strings.HasPrefix(title, "      ") {
		t.Errorf("the indent should stay outside the link, got %q", title)
	}
}

func TestBrowseItemRenderer_PreviewExcerptWithoutURLIsNotLinked(t *testing.T) {
	title := newPreviewRenderer().Title(previewItem("Consider using TRY() here", ""))

	if strings.Contains(title, "\x1b]8;;") {
		t.Errorf("excerpt with no URL should carry no OSC8 link, got %q", title)
	}
}

func TestBrowseItemRenderer_PreviewExcerptTruncatesOnRuneBoundary(t *testing.T) {
	// A body of multi-byte runes is far longer in bytes than in columns, so a
	// byte-based cap slices a rune in half and renders as mojibake.
	body := strings.Repeat("\u65e5", 100)

	plain := stripTerminalEscapes(newPreviewRenderer().Title(previewItem(body, "")))

	if !utf8.ValidString(plain) {
		t.Errorf("excerpt truncation split a rune: %q", plain)
	}
}

// Fixtures mirror real CodeRabbit review comment bodies.
func crBody(lines ...string) string { return strings.Join(lines, "\n") }

func TestCoderabbitSummary(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantSeverity string
		wantTitle    string
		wantOK       bool
	}{
		{
			name: "title on the third line",
			body: crBody(
				"_\U0001FA7A Stability & Availability_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
				"",
				"**Grant Accessibility to the Python executable.**",
				"",
				"The macOS branch calls setFocus after every tree update."),
			wantSeverity: "\U0001F7E0 Major",
			wantTitle:    "Grant Accessibility to the Python executable.",
			wantOK:       true,
		},
		{
			name: "title follows a collapsed analysis block",
			body: crBody(
				"_\U0001F3AF Functional Correctness_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
				"",
				"<details>",
				"<summary>\U0001F9E9 Analysis chain</summary>",
				"",
				"```shell",
				"rg -n -P -C4 '**/*.h Libraries/LibWeb/'",
				"```",
				"",
				"</details>",
				"",
				"**Track traversal depth instead of limiting child count.**"),
			wantSeverity: "\U0001F7E0 Major",
			wantTitle:    "Track traversal depth instead of limiting child count.",
			wantOK:       true,
		},
		{
			name: "title carries trailing prose on the same line",
			body: crBody(
				"_\U0001FA7A Stability & Availability_ | _\U0001F7E1 Minor_ | _\u26A1 Quick win_",
				"",
				"**Deregistered interfaces leak their QObjects.** Each interface allocates one."),
			wantSeverity: "\U0001F7E1 Minor",
			wantTitle:    "Deregistered interfaces leak their QObjects.",
			wantOK:       true,
		},
		{
			name: "label-style bold headings are skipped for the real title",
			body: crBody(
				"_\U0001F512 Security & Privacy_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
				"",
				"**Sensitive Data Exposure (CWE-200):** Exposure of Sensitive Information",
				"",
				"**Reachability:** External",
				"",
				"**Mask password text in the accessibility tree**",
				"",
				"The tree already skips type=password for node_data.value."),
			wantSeverity: "\U0001F7E0 Major",
			wantTitle:    "Mask password text in the accessibility tree",
			wantOK:       true,
		},
		{
			name: "falls back to the label when every bold span is one",
			body: crBody(
				"_\U0001F512 Security & Privacy_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
				"",
				"**Other (CWE-377):** Insecure Temporary File",
				"",
				"**Reachability:** External"),
			wantSeverity: "\U0001F7E0 Major",
			wantTitle:    "Other (CWE-377):",
			wantOK:       true,
		},
		{
			name:   "coderabbit reply carries no severity line",
			body:   "`@sideshowbarker`, acknowledged " + "\u2014" + " this is cosmetic.",
			wantOK: false,
		},
		{
			name:   "ordinary human comment",
			body:   crBody("This shouldn't be necessary since there's a global find_package now.", "", "Please drop it."),
			wantOK: false,
		},
		{
			name: "severity line with no bold title anywhere",
			body: crBody(
				"_\U0001F3AF Functional Correctness_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
				"",
				"<details>",
				"</details>"),
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity, title, ok := coderabbitSummary(tt.body)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if severity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", severity, tt.wantSeverity)
			}
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestCoderabbitExcerptDropsEmojiWithoutColor(t *testing.T) {
	body := crBody(
		"_\U0001FA7A Stability & Availability_ | _\U0001F7E0 Major_ | _\u26A1 Quick win_",
		"",
		"**Grant Accessibility to the Python executable.**")

	restore := ui.ColorsEnabled()
	defer ui.SetColorEnabled(restore)

	ui.SetColorEnabled(true)
	if got, want := coderabbitExcerpt(body), "\U0001F7E0 Major: Grant Accessibility to the Python executable."; got != want {
		t.Errorf("with color: got %q, want %q", got, want)
	}

	ui.SetColorEnabled(false)
	if got, want := coderabbitExcerpt(body), "Major: Grant Accessibility to the Python executable."; got != want {
		t.Errorf("without color: got %q, want %q", got, want)
	}
}

func TestCoderabbitExcerptIgnoresOtherComments(t *testing.T) {
	if got := coderabbitExcerpt("Just a normal review comment."); got != "" {
		t.Errorf("non-CodeRabbit body should yield no excerpt, got %q", got)
	}
}
