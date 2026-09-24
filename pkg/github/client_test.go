package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeGhEnv makes the test binary run fakeGh instead of its tests.
const fakeGhEnv = "GH_REVIEW_RESPONDER_FAKE_GH"

func TestMain(m *testing.M) {
	if os.Getenv(fakeGhEnv) != "" {
		os.Exit(fakeGh(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// useFakeGh points go-gh's GH_PATH at the test binary itself, so every gh call the test makes runs fakeGh.
func useFakeGh(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_PATH", executable)
	t.Setenv(fakeGhEnv, "1")
}

var (
	pageInfoSelection    = regexp.MustCompile(`\bpageInfo\s*\{([^}]*)\}`)
	endCursorDeclaration = regexp.MustCompile(`\$endCursor\s*:\s*String\b`)
	endCursorArgument    = regexp.MustCompile(`\bafter\s*:\s*\$endCursor\b`)
)

// fakeGh answers the gh api calls FetchReviewComments makes for PR 1 of octo-org/octo-repo, printing the responses in
// testdata the way gh prints them. The PR's review threads fill two GraphQL pages. And gh --paginate follows a
// connection past its first page only when the query takes $endCursor as the connection's after: argument and
// selects its pageInfo { hasNextPage endCursor } — so fakeGh prints the second page only for such a query, right
// after the first, with nothing between them.
func fakeGh(args []string) int {
	if len(args) < 2 || args[0] != "api" {
		fmt.Fprintf(os.Stderr, "fake gh: unexpected arguments %q\n", args)
		return 1
	}

	var responses []string
	switch endpoint := args[1]; endpoint {
	case "graphql":
		responses = []string{"testdata/review-threads-page-1.json"}
		if slices.Contains(args, "--paginate") && followsPages(graphQLQuery(args)) {
			responses = append(responses, "testdata/review-threads-page-2.json")
		}
	case "repos/octo-org/octo-repo/pulls/1/comments":
		responses = []string{"testdata/review-comments.json"}
	default:
		fmt.Fprintf(os.Stderr, "fake gh: unexpected endpoint %q\n", endpoint)
		return 1
	}

	for _, response := range responses {
		data, err := os.ReadFile(response)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake gh: %v\n", err)
			return 1
		}
		// gh prints each response as GitHub sends it: compact JSON.
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, data); err != nil {
			fmt.Fprintf(os.Stderr, "fake gh: %s: %v\n", response, err)
			return 1
		}
		fmt.Print(compacted.String())
	}
	return 0
}

// graphQLQuery returns the query given to gh api graphql as -f query=...
func graphQLQuery(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if query, ok := strings.CutPrefix(args[i+1], "query="); ok && args[i] == "-f" {
			return query
		}
	}
	return ""
}

// followsPages reports if gh --paginate can follow query from one page of a connection to the next.
func followsPages(query string) bool {
	selection := pageInfoSelection.FindStringSubmatch(query)
	return selection != nil &&
		strings.Contains(selection[1], "hasNextPage") &&
		strings.Contains(selection[1], "endCursor") &&
		endCursorDeclaration.MatchString(query) &&
		endCursorArgument.MatchString(query)
}

func TestExtractGitHubOwner(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		want      string
	}{
		{
			name:      "SSH format",
			remoteURL: "git@github.com:sideshowbarker/WebKit.git",
			want:      "sideshowbarker",
		},
		{
			name:      "SSH format with trailing newline",
			remoteURL: "git@github.com:user/repo.git\n",
			want:      "user",
		},
		{
			name:      "HTTPS format",
			remoteURL: "https://github.com/WebKit/WebKit.git",
			want:      "WebKit",
		},
		{
			name:      "HTTPS format without .git",
			remoteURL: "https://github.com/owner/repo",
			want:      "owner",
		},
		{
			name:      "non-GitHub URL",
			remoteURL: "git@gitlab.com:user/repo.git",
			want:      "",
		},
		{
			name:      "empty string",
			remoteURL: "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGitHubOwner(tt.remoteURL)
			if got != tt.want {
				t.Errorf("extractGitHubOwner(%q) = %q, want %q", tt.remoteURL, got, tt.want)
			}
		})
	}
}

// FetchReviewComments has to take thread info from every page of a PR's review threads, not just the first — a
// comment whose thread it misses reads as unresolved, and the thread's replies get listed as comments of their own.
func TestFetchReviewCommentsReadsEveryReviewThreadPage(t *testing.T) {
	useFakeGh(t)
	client := NewClient()
	client.SetRepo("octo-org/octo-repo")

	comments, err := client.FetchReviewComments(1)
	if err != nil {
		t.Fatalf("FetchReviewComments(1): %v", err)
	}

	type thread struct {
		CommentID int64
		ThreadID  string
		Resolved  bool
		ReplyIDs  []int64
	}
	var got []thread
	for _, comment := range comments {
		var replyIDs []int64
		for _, reply := range comment.ThreadComments {
			replyIDs = append(replyIDs, reply.ID)
		}
		got = append(got, thread{comment.ID, comment.ThreadID, comment.IsResolved(), replyIDs})
	}
	want := []thread{
		{CommentID: 1001, ThreadID: "PRRT_first_page", Resolved: true},
		{CommentID: 2001, ThreadID: "PRRT_second_page", Resolved: true, ReplyIDs: []int64{2002}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FetchReviewComments(1) returned\n%+v\nwant\n%+v", got, want)
	}
}
