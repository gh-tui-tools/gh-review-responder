package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gh-tui-tools/gh-review-responder/pkg/github"
)

func TestFindReplacementTargetByLineRange(t *testing.T) {
	a := New()
	fileLines := []string{"line1", "line2", "line3", "line4", "line5"}

	tests := []struct {
		name            string
		comment         *github.ReviewComment
		wantTargetLine  int
		wantRemoveCount int
		wantErr         bool
	}{
		{
			name: "single-line suggestion (StartLine is 0)",
			comment: &github.ReviewComment{
				Line:      3,
				StartLine: 0,
			},
			wantTargetLine:  2,
			wantRemoveCount: 1,
		},
		{
			name: "multi-line suggestion",
			comment: &github.ReviewComment{
				Line:      4,
				StartLine: 2,
			},
			wantTargetLine:  1,
			wantRemoveCount: 3,
		},
		{
			name: "single-line with StartLine equal to Line",
			comment: &github.ReviewComment{
				Line:      1,
				StartLine: 1,
			},
			wantTargetLine:  0,
			wantRemoveCount: 1,
		},
		{
			name: "last line of file",
			comment: &github.ReviewComment{
				Line:      5,
				StartLine: 0,
			},
			wantTargetLine:  4,
			wantRemoveCount: 1,
		},
		{
			name: "entire file range",
			comment: &github.ReviewComment{
				Line:      5,
				StartLine: 1,
			},
			wantTargetLine:  0,
			wantRemoveCount: 5,
		},
		{
			name: "line beyond file length",
			comment: &github.ReviewComment{
				Line:      6,
				StartLine: 0,
			},
			wantErr: true,
		},
		{
			name: "range exceeds file length",
			comment: &github.ReviewComment{
				Line:      6,
				StartLine: 4,
			},
			wantErr: true,
		},
		{
			name: "line zero (invalid)",
			comment: &github.ReviewComment{
				Line:      0,
				StartLine: 0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetLine, removeCount, err := a.findReplacementTargetByLineRange(tt.comment, fileLines)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got targetLine=%d, removeCount=%d", targetLine, removeCount)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if targetLine != tt.wantTargetLine {
				t.Errorf("targetLine = %d, want %d", targetLine, tt.wantTargetLine)
			}
			if removeCount != tt.wantRemoveCount {
				t.Errorf("removeCount = %d, want %d", removeCount, tt.wantRemoveCount)
			}
		})
	}
}

func TestPreviewSuggestion(t *testing.T) {
	// Create a temp file with known content
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

	a := New()

	tests := []struct {
		name           string
		comment        *github.ReviewComment
		wantContains   []string
		wantNoContains []string
		wantErr        bool
	}{
		{
			name: "single-line replacement",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     0,
				SuggestedCode: "\tfmt.Println(\"world\")\n",
			},
			wantContains: []string{
				"--- a/",
				"+++ b/",
				"@@ -4,1 +4,1 @@",
				"-\tfmt.Println(\"hello\")",
				"+\tfmt.Println(\"world\")",
			},
		},
		{
			name: "multi-line replacement",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     3,
				SuggestedCode: "func greet(name string) {\n\tfmt.Printf(\"hello %s\\n\", name)\n",
			},
			wantContains: []string{
				"@@ -3,2 +3,2 @@",
				"-func hello() {",
				"-\tfmt.Println(\"hello\")",
				"+func greet(name string) {",
				"+\tfmt.Printf(\"hello %s\\n\", name)",
			},
		},
		{
			name: "expanding replacement (1 line to 3)",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     0,
				SuggestedCode: "\tlog.Println(\"a\")\n\tlog.Println(\"b\")\n\tlog.Println(\"c\")\n",
			},
			wantContains: []string{
				"@@ -4,1 +4,3 @@",
				"-\tfmt.Println(\"hello\")",
				"+\tlog.Println(\"a\")",
				"+\tlog.Println(\"b\")",
				"+\tlog.Println(\"c\")",
			},
		},
		{
			name: "shrinking replacement (2 lines to 1)",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     3,
				SuggestedCode: "func hello() { fmt.Println(\"hello\") }\n",
			},
			wantContains: []string{
				"@@ -3,2 +3,1 @@",
				"-func hello() {",
				"-\tfmt.Println(\"hello\")",
				"+func hello() { fmt.Println(\"hello\") }",
			},
		},
		{
			name: "first line replacement",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          1,
				StartLine:     0,
				SuggestedCode: "package greet\n",
			},
			wantContains: []string{
				"@@ -1,1 +1,1 @@",
				"-package main",
				"+package greet",
			},
		},
		{
			name: "empty suggestion shows pure deletion",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     0,
				SuggestedCode: "",
			},
			wantContains: []string{
				"@@ -4,1 +4,0 @@",
				"-\tfmt.Println(\"hello\")",
			},
		},
		{
			name: "empty suggestion deletes multi-line range",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          4,
				StartLine:     3,
				SuggestedCode: "",
			},
			wantContains: []string{
				"@@ -3,2 +3,0 @@",
				"-func hello() {",
				"-\tfmt.Println(\"hello\")",
			},
		},
		{
			name: "nonexistent file",
			comment: &github.ReviewComment{
				Path:          filepath.Join(dir, "nonexistent.go"),
				Line:          1,
				StartLine:     0,
				SuggestedCode: "replacement\n",
			},
			wantErr: true,
		},
		{
			name: "line out of range",
			comment: &github.ReviewComment{
				Path:          filePath,
				Line:          99,
				StartLine:     0,
				SuggestedCode: "replacement\n",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := a.PreviewSuggestion(tt.comment)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got diff:\n%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(diff, s) {
					t.Errorf("diff missing %q\ngot:\n%s", s, diff)
				}
			}
			for _, s := range tt.wantNoContains {
				if strings.Contains(diff, s) {
					t.Errorf("diff should not contain %q\ngot:\n%s", s, diff)
				}
			}
		})
	}
}

func TestApplySuggestion(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		comment       *github.ReviewComment // Path is filled in by test harness
		wantContent   string
		wantContains  []string
		wantAbsent    []string
	}{
		{
			name:        "single-line replacement (StartLine=0)",
			fileContent: "package main\n\nfunc hello() {\n\tfmt.Println(\"hello\")\n}\n",
			comment: &github.ReviewComment{
				Line:          4,
				StartLine:     0,
				SuggestedCode: "\tfmt.Println(\"world\")\n",
			},
			wantContains: []string{"fmt.Println(\"world\")"},
			wantAbsent:   []string{"fmt.Println(\"hello\")"},
		},
		{
			name:        "multi-line replacement (3 lines to 2)",
			fileContent: "line1\nline2\nline3\nline4\nline5\n",
			comment: &github.ReviewComment{
				Line:          4,
				StartLine:     2,
				SuggestedCode: "replaced_a\nreplaced_b\n",
			},
			wantContent: "line1\nreplaced_a\nreplaced_b\nline5\n",
		},
		{
			name:        "multi-line replacement expands (1 line to 3)",
			fileContent: "aaa\nbbb\nccc\n",
			comment: &github.ReviewComment{
				Line:          2,
				StartLine:     2,
				SuggestedCode: "x1\nx2\nx3\n",
			},
			wantContent: "aaa\nx1\nx2\nx3\nccc\n",
		},
		{
			name:        "multi-line replacement shrinks (3 lines to 1)",
			fileContent: "aaa\nbbb\nccc\nddd\neee\n",
			comment: &github.ReviewComment{
				Line:          4,
				StartLine:     2,
				SuggestedCode: "only_one\n",
			},
			wantContent: "aaa\nonly_one\neee\n",
		},
		{
			name:        "replace first line",
			fileContent: "old_first\nsecond\nthird\n",
			comment: &github.ReviewComment{
				Line:          1,
				StartLine:     0,
				SuggestedCode: "new_first\n",
			},
			wantContent: "new_first\nsecond\nthird\n",
		},
		{
			name:        "replace last line",
			fileContent: "first\nsecond\nold_last\n",
			comment: &github.ReviewComment{
				Line:          3,
				StartLine:     0,
				SuggestedCode: "new_last\n",
			},
			wantContent: "first\nsecond\nnew_last\n",
		},
		{
			name:        "diff hunk is irrelevant to replacement",
			fileContent: "aaa\nbbb\nccc\n",
			comment: &github.ReviewComment{
				Line:      2,
				StartLine: 0,
				// DiffHunk references completely different content;
				// ApplySuggestion uses line ranges, not diff hunk parsing
				DiffHunk:      "@@ -1,3 +1,3 @@\n zzz\n-wrong\n+also_wrong\n yyy",
				SuggestedCode: "BBB\n",
			},
			wantContent: "aaa\nBBB\nccc\n",
		},
		{
			name:        "empty suggestion deletes multi-line range",
			fileContent: "aaa\nbbb\nccc\nddd\neee\n",
			comment: &github.ReviewComment{
				Line:          4,
				StartLine:     2,
				SuggestedCode: "",
			},
			wantContent: "aaa\neee\n",
		},
		{
			name:        "preserves trailing newline",
			fileContent: "aaa\nbbb\n",
			comment: &github.ReviewComment{
				Line:          1,
				StartLine:     0,
				SuggestedCode: "AAA\n",
			},
			wantContent: "AAA\nbbb\n",
		},
		{
			name:        "empty suggestion deletes lines",
			fileContent: "keep\ndelete_me\nalso_keep\n",
			comment: &github.ReviewComment{
				Line:          2,
				StartLine:     0,
				SuggestedCode: "",
			},
			wantContent: "keep\nalso_keep\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			filePath := filepath.Join(dir, "test.go")
			if err := os.WriteFile(filePath, []byte(tt.fileContent), 0o644); err != nil {
				t.Fatal(err)
			}

			origDir, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(origDir) }()
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}

			a := New()
			comment := tt.comment
			comment.Path = filePath

			if err := a.ApplySuggestion(comment); err != nil {
				t.Fatalf("ApplySuggestion failed: %v", err)
			}

			got, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatal(err)
			}

			if tt.wantContent != "" && string(got) != tt.wantContent {
				t.Errorf("file content mismatch\nwant: %q\ngot:  %q", tt.wantContent, string(got))
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(string(got), s) {
					t.Errorf("file missing %q\ngot:\n%s", s, string(got))
				}
			}
			for _, s := range tt.wantAbsent {
				if strings.Contains(string(got), s) {
					t.Errorf("file should not contain %q\ngot:\n%s", s, string(got))
				}
			}
		})
	}
}

func TestFindReplacementTarget(t *testing.T) {
	a := New()

	tests := []struct {
		name            string
		fileLines       []string
		comment         *github.ReviewComment
		wantTargetLine  int
		wantRemoveCount int
		wantErr         bool
		wantErrContains string
	}{
		{
			name:      "strategy 1: position mapping finds match",
			fileLines: []string{"aaa", "bbb", "ccc"},
			comment: &github.ReviewComment{
				DiffHunk: "@@ -1,3 +1,3 @@\n aaa\n-old\n+bbb\n ccc",
			},
			wantTargetLine:  1,
			wantRemoveCount: 1,
		},
		{
			name:      "strategy 1: multi-line added lines match",
			fileLines: []string{"aaa", "b1", "b2", "ccc"},
			comment: &github.ReviewComment{
				DiffHunk: "@@ -1,2 +1,4 @@\n aaa\n-old\n+b1\n+b2\n ccc",
			},
			wantTargetLine:  1,
			wantRemoveCount: 2,
		},
		{
			name:      "strategy 2: position mismatch, content found elsewhere",
			fileLines: []string{"xxx", "yyy", "bbb", "zzz"},
			comment: &github.ReviewComment{
				// Position mapping points to index 1, but "bbb" is at index 2
				DiffHunk: "@@ -1,3 +1,3 @@\n aaa\n-old\n+bbb\n ccc",
			},
			wantTargetLine:  2,
			wantRemoveCount: 1,
		},
		{
			name:      "no added lines in diff hunk",
			fileLines: []string{"aaa", "ccc"},
			comment: &github.ReviewComment{
				DiffHunk: "@@ -1,3 +1,2 @@\n aaa\n-old\n ccc",
			},
			wantErr:         true,
			wantErrContains: "no added lines found",
		},
		{
			name:      "empty diff hunk",
			fileLines: []string{"aaa", "bbb"},
			comment: &github.ReviewComment{
				DiffHunk: "",
			},
			wantErr:         true,
			wantErrContains: "no added lines found",
		},
		{
			name:      "content not found anywhere in file",
			fileLines: []string{"xxx", "yyy", "zzz"},
			comment: &github.ReviewComment{
				DiffHunk: "@@ -1,3 +1,3 @@\n aaa\n-old\n+notfound\n ccc",
			},
			wantErr:         true,
			wantErrContains: "could not find the code to replace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetLine, removeCount, err := a.findReplacementTarget(tt.comment, tt.fileLines)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got targetLine=%d, removeCount=%d", targetLine, removeCount)
				} else if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if targetLine != tt.wantTargetLine {
				t.Errorf("targetLine = %d, want %d", targetLine, tt.wantTargetLine)
			}
			if removeCount != tt.wantRemoveCount {
				t.Errorf("removeCount = %d, want %d", removeCount, tt.wantRemoveCount)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "relative path within repo",
			path:    "pkg/applier/applier.go",
			wantErr: false,
		},
		{
			name:    "path traversal with dotdot",
			path:    "../../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "path traversal hidden in middle",
			path:    "pkg/../../../../../../etc/shadow",
			wantErr: true,
		},
		{
			name:    "absolute path outside repo",
			path:    "/etc/passwd",
			wantErr: true,
		},
		{
			name:    "absolute path outside repo (home dir)",
			path:    "/tmp/malicious-file",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if tt.wantErr && err == nil {
				t.Errorf("validatePath(%q) = nil, want error", tt.path)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validatePath(%q) = %v, want nil", tt.path, err)
			}
		})
	}
}

func TestApplySuggestionRejectsTraversal(t *testing.T) {
	a := New()
	comment := &github.ReviewComment{
		Path:          "../../../etc/passwd",
		Line:          1,
		StartLine:     0,
		SuggestedCode: "malicious content",
	}

	err := a.ApplySuggestion(comment)
	if err == nil {
		t.Fatal("ApplySuggestion should reject path traversal")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("error should mention path is outside repository, got: %v", err)
	}
}

func TestPreviewSuggestionRejectsTraversal(t *testing.T) {
	a := New()
	comment := &github.ReviewComment{
		Path:          "../../../etc/passwd",
		Line:          1,
		StartLine:     0,
		SuggestedCode: "malicious content",
	}

	_, err := a.PreviewSuggestion(comment)
	if err == nil {
		t.Fatal("PreviewSuggestion should reject path traversal")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("error should mention path is outside repository, got: %v", err)
	}
}
