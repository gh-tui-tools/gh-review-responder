# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`gh-review-responder` is a GitHub CLI extension written in Go that applies GitHub pull request review comments and suggestions directly to local files. It fetches review comments, parses suggestion blocks, and applies them interactively.

## Build and Test Commands

```bash
# Build the binary
go build

# Run tests
go test -v ./...

# Run tests with coverage
go test -v -coverprofile=coverage.out ./...

# Format code (uses gofumpt)
make fmt

# Run linter
make lint

# Install as gh extension (after building)
gh extension install .

# Uninstall extension
gh extension remove review-responder
```

## Architecture

### Core Components

**GitHub API Client** (`pkg/github/client.go`)
- Fetches PR review comments using both REST and GraphQL APIs
- GraphQL: Retrieves thread information and resolved status
- REST: Gets detailed comment data including diff hunks and position metadata
- Populates `ReviewComment` struct with fields: `Line`, `OriginalLine`, `StartLine`, `EndLine`, `DiffHunk`, `DiffSide` (LEFT/RIGHT), `IsOutdated`
- Thread management: Maps review threads to top-level comments, filters out reply comments

**Diff Parsing** (`pkg/diffhunk/diffhunk.go`)
- Parses unified diff format (`@@ -oldStart,oldLines +newStart,newLines @@`)
- `DiffLine` tracks: `Type` (Context/Add/Delete), `OldLineNumber`, `NewLineNumber`, `Text`, `PositionInHunk`
- `GetAddedLines()` and `GetRemovedLines()` extract specific line types from hunks
- All line numbers are 1-based internally (use `GetZeroBased()` for 0-based conversion)

**Position Mapping** (`pkg/diffposition/diffposition.go`)
- Maps line numbers between old and new file versions through diff hunks
- `MapOldPositionToNew()`: old file line → new file line (returns -1 if deleted)
- `MapNewPositionToOld()`: new file line → old file line (returns -1 if added)
- `CalculateCommentPosition()`: Determines if comment is outdated based on position mapping
- `DiffSide`: LEFT (base/original) vs RIGHT (modified/new)

**Suggestion Applier** (`pkg/applier/applier.go`)
- Two-strategy approach for finding target lines:
  1. **Position mapping** (primary): Uses parsed diff hunk to find first added line's position
  2. **Content matching** (fallback): Searches for exact content match in current file
- Creates unified diff patches and applies via `git apply --unidiff-zero`
- Debug mode: Set with `SetDebug(true)`, logs to stderr
- On content mismatch: Generates diagnostic diff file to `/tmp/gh-review-responder-mismatch-<ID>.diff`
- On `git apply` failure: Saves patch to `/tmp/gh-review-responder-patch-<ID>.patch`

**Suggestion Parser** (`pkg/parser/suggestion.go`)
- Extracts code from GitHub suggestion blocks (` ```suggestion ... ``` `)

**AI Integration** (`pkg/ai/`)
- AI-powered suggestion application for cases where traditional matching fails
- Provider interface supports multiple AI backends (Gemini, OpenAI, Claude, Ollama)
- Template system with embedded defaults, customizable via filesystem
- Gathers comprehensive context: review comment, diff hunk, current file, expected lines
- Returns unified diff patch with explanation, confidence score, and warnings
- See [docs/AI_INTEGRATION.md](docs/AI_INTEGRATION.md) for full details

**UI Components** (`pkg/ui/`)
- Terminal rendering, colored diff output, hyperlinks (OSC8), markdown rendering

### CLI Commands

- `gh review-responder list [PR_NUMBER] [THREAD_ID]` - List unresolved review comments (use `--all` for resolved too)
  - Flags: `-R/--repo <owner/repo>` (specify different repo), `--json` (raw review comment JSON for optional thread), `--code-context` (show diff hunk in output)
- `gh review-responder apply [PR_NUMBER]` - Interactive mode to apply suggestions
  - Flags: `--all` (auto-apply all), `--file <path>`, `--include-resolved`, `--debug`
  - AI Flags: `--ai-auto` (apply all with AI), `--ai-provider <gemini>`, `--ai-model <model>`, `--ai-template <path>`, `--ai-token <key>`
  - Interactive: Select 'a' option to use AI for individual suggestions

### Debugging

When issues occur applying suggestions:

1. Enable debug mode with `--debug` flag for detailed output
2. Check diagnostic files in `/tmp/`:
   - `gh-review-responder-mismatch-*.diff` - Shows expected vs actual content with proper unified diff format
   - `gh-review-responder-patch-*.patch` - Contains failed patch with error details
   - `gh-review-responder-ai-patch-*.patch` - Contains failed AI-generated patch with metadata
3. Use `gh review-responder list <PR> [THREAD_ID] --json` to see raw GitHub API response (include `THREAD_ID` to limit to a thread)

See [DEBUGGING.md](DEBUGGING.md) for detailed troubleshooting guide.
See [docs/AI_INTEGRATION.md](docs/AI_INTEGRATION.md) for AI feature documentation.

### Key Implementation Details

- GitHub API provides: `line` (new), `original_line` (old), `start_line`, `side` (LEFT/RIGHT), `diff_hunk`
- Comments can be outdated if the file changed since review (detected via position mapping)
- Suggestion application matches content from diff hunk's added lines (`+` prefix) against current file
- Patch format uses `git apply --unidiff-zero` for zero-context diffs
- Thread replies are fetched separately via GraphQL and attached to top-level comments
- Resolved status comes from GraphQL `isResolved` field on review threads

## Development Notes

- Uses `github.com/cli/go-gh/v2` for GitHub CLI integration
- Formatting: Project uses `gofumpt` (stricter than `gofmt`)
- Line number convention: 1-based everywhere except when explicitly converting for array indexing
- Error handling: Provide clear error messages with file paths and line numbers for debugging
