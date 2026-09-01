# gh-review-responder Design Document

This document describes the architecture, commands, and features of `gh-review-responder`, a GitHub CLI extension for managing pull request review comments.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Commands](#commands)
  - [browse](#browse-command)
  - [apply](#apply-command)
  - [list](#list-command)
  - [comment](#comment-command)
  - [resolve](#resolve-command)
- [Package Structure](#package-structure)
- [Data Flow](#data-flow)
- [Performance Optimizations](#performance-optimizations)

---

## Overview

`gh-review-responder` helps developers work with GitHub pull request review comments directly from the terminal. It provides:

- **Interactive browsing** of review comments with keyboard navigation
- **Automatic suggestion application** to local files
- **Reply and resolve** capabilities without leaving the terminal
- **AI-assisted** suggestion application for complex cases
- **Editor integration** for composing replies
- **Emoji reactions** for acknowledging comments quickly

```
┌───────────────────────────────────────────────────────────────────────┐
│                         gh-review-responder                           │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐      │
│  │ browse  │  │  apply  │  │  list   │  │ comment │  │ resolve │      │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘      │
│       │            │            │            │            │           │
│       └────────────┴────────────┴────────────┴────────────┘           │
│                                 │                                     │
│                         ┌───────┴───────┐                             │
│                         │  GitHub API   │                             │
│                         │  (GraphQL +   │                             │
│                         │    REST)      │                             │
│                         └───────────────┘                             │
│                                                                       │
└───────────────────────────────────────────────────────────────────────┘
```

---

## Architecture

### High-Level Component Diagram

```
┌───────────────────────────────────────────────────────────────────────┐
│                            CLI Layer (cmd/)                           │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┬──────────┐  │
│  │   root   │  browse  │  apply   │   list   │ comment  │ resolve  │  │
│  └────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬─────┘  │
└───────┼──────────┼──────────┼──────────┼──────────┼──────────┼────────┘
        │          │          │          │          │          │
┌───────┼──────────┼──────────┼──────────┼──────────┼──────────┼────────┐
│       │          │          │          │          │          │        │
│       ▼          ▼          ▼          ▼          ▼          ▼        │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │                          pkg/ui                                │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────┐     │   │
│  │  │ selector │  │  colors  │  │  quote   │  │ pr_selector │     │   │
│  │  │   (TUI)  │  │ (render) │  │  (fmt)   │  │             │     │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └─────────────┘     │   │
│  └────────────────────────────────────────────────────────────────┘   │
│                                                                       │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │                        pkg/github                              │   │
│  │  ┌──────────────────────────────────────────────────────────┐  │   │
│  │  │  Client: FetchReviewComments, ReplyToReviewComment,      │  │   │
│  │  │          ResolveThread, UnresolveThread, GetCurrentPR    │  │   │
│  │  └──────────────────────────────────────────────────────────┘  │   │
│  └────────────────────────────────────────────────────────────────┘   │
│                                                                       │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐  ┌────────────┐  │
│  │ pkg/applier  │  │ pkg/diffhunk │  │ pkg/diffpos │  │ pkg/parser │  │
│  │ (apply code) │  │ (parse diffs)│  │ (map lines) │  │ (suggest)  │  │
│  └──────────────┘  └──────────────┘  └─────────────┘  └────────────┘  │
│                                                                       │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │                          pkg/ai                                │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │   │
│  │  │ provider │  │  gemini  │  │ prompts  │  │  config  │        │   │
│  │  │  (intf)  │  │  (impl)  │  │ (tmpls)  │  │          │        │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │   │
│  └────────────────────────────────────────────────────────────────┘   │
│                                                                       │
│                         Package Layer (pkg/)                          │
└───────────────────────────────────────────────────────────────────────┘
```

### GitHub API Integration

The tool uses both GraphQL and REST APIs:

- **GraphQL**: Thread information, resolved status, mutations (resolve/unresolve)
- **REST**: Detailed comment data including diff hunks, positions, and line numbers

```
┌───────────────────────┐         ┌─────────────────┐
│  gh-review-responder  │         │   GitHub API    │
│                       │         │                 │
│  ┌───────────┐        │  REST   │  ┌───────────┐  │
│  │  Client   │────────┼────────▶│  │ /pulls/   │  │
│  │           │        │         │  │ comments  │  │
│  │           │        │ GraphQL │  │           │  │
│  │           │────────┼────────▶│  │ threads   │  │
│  │           │        │         │  │ mutations │  │
│  └───────────┘        │         │  └───────────┘  │
└───────────────────────┘         └─────────────────┘
```

---

## Commands

### browse Command

The default and most feature-rich command. Provides an interactive TUI for browsing and acting on review comments.

**Usage:**
```bash
gh review-responder browse [PR_NUMBER] [COMMENT_ID]
gh review-responder                    # browse is the default command
```

**Flags:**
- `--debug` - Enable debug output

#### Views

The browse command provides two interactive views:

```
┌───────────────────────────────────────────────────────────────────────┐
│                            LIST VIEW                                  │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  > src/components/Button.tsx                                          │
│      @reviewer Line 42 [2 replies]                     (unresolved)   │
│      @author Line 15                                   (resolved)     │
│                                                                       │
│  > src/utils/format.ts                                                │
│      @reviewer Line 8 [1 reply]                        (unresolved)   │
│                                                                       │
├───────────────────────────────────────────────────────────────────────┤
│  arrows:navigate  enter:view  o:open  r:resolve  Q:quote  a:agent     │
└───────────────────────────────────────────────────────────────────────┘

                              │ Enter
                              ▼

┌───────────────────────────────────────────────────────────────────────┐
│                              DETAIL VIEW                              │
└───────────────────────────────────────────────────────────────────────┘
  Author: @reviewer
  Location: src/components/Button.tsx:42
  Status: unresolved
  URL: https://github.com/owner/repo/pull/123#discussion_r789
  Time: 2 hours ago

  --- Comment ---
  Consider using React.memo here to prevent unnecessary re-renders.
  Reactions: 👍 3 ❤️ 1

  --- Context ---
  @@ -40,5 +40,7 @@
   export function Button({ onClick, children }) {
  +  const handleClick = useCallback(() => {
  +    onClick?.();

  --- Replies (2) ---
  Reply 1 by @author | 1 hour ago
  Good suggestion, I'll look into it.
  Reactions: 👍 1
┌───────────────────────────────────────────────────────────────────────┐
│  esc:back  o:open  r:resolve  R:resolve+comment  Q:quote  a:agent     │
└───────────────────────────────────────────────────────────────────────┘
```

#### Key Bindings

| Key | List View | Detail View | Description |
|-----|-----------|-------------|-------------|
| `q` | Quit | Back to list | Exit or go back |
| `esc` | - | Back to list | Go back |
| `enter` | View detail | - | Show full comment |
| `o` | Open in browser | Open in browser | Open comment URL |
| `r`/`u` | Toggle resolve | Toggle resolve | Resolve/unresolve thread |
| `R`/`U` | Resolve+comment | Resolve+comment | Resolve with editor reply |
| `Q` | Quote reply | Quote reply | Reply quoting comment |
| `C` | Quote+context | Quote+context | Reply with diff context |
| `a` | Launch agent | Launch agent | Hand off to coding agent |
| `e` | Edit file | Edit file | Open file at line |
| `x` | React | React | Add emoji reaction |
| `h`/`tab` | Toggle filter | - | Show/hide resolved |
| `i` | Refresh | Refresh | Fetch fresh data |
| `Ctrl+F` | - | Page down | Scroll viewport |
| `Ctrl+B` | - | Page up | Scroll viewport |

#### Thread Comment Selection

When a thread has multiple comments, pressing `Q`, `C`, `a`, or `x` enters selection mode:

```
┌───────────────────────────────────────────────────────────────────────┐
│                       THREAD COMMENT SELECTION                        │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ▶▶▶ SELECTED COMMENT ◀◀◀                                             │
│  --- Comment ---                                                      │
│  Consider using React.memo here...                                    │
│  ▶▶▶ END SELECTED ◀◀◀                                                 │
│                                                                       │
│  --- Replies ---                                                      │
│  Reply 1 by @author | 1 hour ago                                      │
│  Good suggestion...                                                   │
│                                                                       │
├───────────────────────────────────────────────────────────────────────┤
│  [1/3] @reviewer: Consider... (Enter=select, Q=next, Esc=cancel)      │
└───────────────────────────────────────────────────────────────────────┘
```

- Press same key to cycle through comments
- Press Enter to confirm selection
- Press Esc to cancel

#### Quote Reply Feature

```
User presses Q or C
        │
        ▼
┌───────────────────┐
│ Format quoted     │
│ content with      │
│ FormatQuotedReply │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ Create temp file  │
│ with template     │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ Launch $EDITOR    │
│ (TUI suspends)    │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ User edits and    │
│ saves file        │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ POST to GitHub    │
│ API               │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ Show confirmation │
│ dialog            │
└───────────────────┘
```

**Q key format (quote only):**
```markdown
> @author wrote:
>
> [original comment body]

[cursor here for reply]
```

**C key format (quote with context):**
```markdown
> ```diff
> --- a/path/to/file.go
> +++ b/path/to/file.go
> @@ -10,5 +10,7 @@
>  context line
> +added line
> -removed line
> ```
>
> @author wrote:
>
> [original comment body]

[cursor here for reply]
```

#### Coding Agent Integration

The `a` key launches a coding agent with the review comment context:

```bash
# Default: uses 'claude' (Claude Code CLI)
gh review-responder browse 123

# Use a different agent
GH_REVIEW_RESPONDER_AGENT=aider gh review-responder browse 123

# Test prompt format
GH_REVIEW_RESPONDER_AGENT=echo gh review-responder browse 123
```

**Prompt format:**
```
Review comment on <path>:<line>

<full comment body>
```

#### Emoji Reactions Feature

Press `x` to add an emoji reaction to a review comment. This provides a quick way to acknowledge comments without typing a reply.

**Supported emojis:**

| Emoji | Name |
|-------|------|
| 👍 | +1 |
| 👎 | -1 |
| 😄 | laugh |
| 😕 | confused |
| ❤️ | heart |
| 🎉 | hooray |
| 🚀 | rocket |
| 👀 | eyes |

**User flow:**

1. Press `x` on a comment
2. For multi-comment threads, first select which comment (same as Q/C/a)
3. Status bar shows: `React: [1/8] +1 (x=next, Enter=add, Esc=cancel)`
4. Press `x` to cycle through emojis, Enter to add, Esc to cancel
5. Confirmation dialog shows the reaction was added with a link to the comment

**GitHub API:**

```
POST /repos/{owner}/{repo}/pulls/comments/{comment_id}/reactions
{"content": "+1"}
```

#### Reaction Display

All comments display their existing reactions with counts, matching GitHub's web UI:

```
Reactions: 👍 5 👎 1 😄 2 🎉 1 😕 1 ❤️ 3 🚀 2 👀 1
```

- Reactions are shown for both top-level comments and thread replies
- Only reactions with count > 0 are displayed
- Order matches GitHub's display order
- Reactions are fetched from REST API (top-level) and GraphQL (replies)

---

### apply Command

Applies code suggestions from review comments to local files.

**Usage:**
```bash
gh review-responder apply [PR_NUMBER]
```

**Flags:**
- `--all` - Apply all suggestions without prompting
- `--file <path>` - Only apply suggestions for a specific file
- `--include-resolved` - Include resolved suggestions
- `--debug` - Enable debug output
- `--ai-auto` - Automatically apply all using AI
- `--ai-provider <name>` - AI provider (gemini, openai, claude, ollama)
- `--ai-model <model>` - AI model to use
- `--ai-template <path>` - Custom prompt template
- `--ai-token <key>` - API token

#### Apply Flow

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ Fetch review    │────▶│ Filter comments │────▶│ For each        │
│ comments        │     │ with suggestions│     │ suggestion:     │
└─────────────────┘     └─────────────────┘     └────────┬────────┘
                                                         │
                        ┌────────────────────────────────┘
                        ▼
              ┌─────────────────┐
              │ Try position    │
              │ mapping         │
              └────────┬────────┘
                       │
           ┌───────────┴───────────┐
           │                       │
     Found match?            No match
           │                       │
           ▼                       ▼
    ┌─────────────┐         ┌─────────────┐
    │ Generate    │         │ Try content │
    │ unified     │         │ matching    │
    │ diff patch  │         │ (fallback)  │
    └──────┬──────┘         └──────┬──────┘
           │                       │
           ▼                       ▼
    ┌─────────────┐         ┌─────────────┐
    │ Apply via   │         │ AI-assisted │
    │ git apply   │         │ application │
    └─────────────┘         └─────────────┘
```

#### Interactive Mode

When run without `--all`, presents an interactive menu:

```
[1/5] src/utils/format.ts:42 by @reviewer

Review comment:
  This could be simplified using template literals.

Suggested change:
  const msg = `Hello, ${name}!`;

Apply this suggestion? [y/n/a/q/?]
  y = yes, apply this suggestion
  n = no, skip this suggestion
  a = apply with AI assistance
  q = quit
  ? = help
```

---

### list Command

Lists review comments in various formats.

**Usage:**
```bash
gh review-responder list [PR_NUMBER] [THREAD_ID]
```

**Flags:**
- `--all` - Include resolved comments
- `--debug` - Enable debug output
- `--llm` - Output in LLM-friendly format
- `--json` - Output raw JSON
- `--code-context` - Include diff hunks

#### Output Formats

**Default format:**
```
[1/3] src/Button.tsx:42 by @reviewer (ID 123456789)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Reactions: 👍 3 ❤️ 1

Review comment:
  Consider using React.memo here to prevent unnecessary re-renders.

Suggested change:
  export const Button = React.memo(({ onClick, children }) => {

Thread replies:
  └─ Reply 1 by @author:
     Reactions: 👍 1
     Good point, I'll update this.
```

**LLM format (`--llm`):**
```
FILE: src/Button.tsx:42
COMMENT_ID: 123456789
AUTHOR: reviewer
URL: https://github.com/...
STATUS: unresolved
COMMENT:
Consider using React.memo here...
SUGGESTION:
export const Button = React.memo(...)
REPLIES:
  [1] author: Good point...
```

**JSON format (`--json`):**
Raw GitHub API response for the specified comments.

---

### comment Command

Posts a reply to a review comment thread.

**Usage:**
```bash
gh review-responder comment COMMENT_ID [PR_NUMBER]
```

**Flags:**
- `--body <text>` - Comment body
- `--body-file <path>` - Read body from file
- `--stdin` - Read body from stdin
- `--resolve` - Resolve thread after replying
- `--debug` - Enable debug output

#### Comment Flow

```
┌─────────────────┐
│ Resolve body    │
│ from flag/file/ │
│ stdin/editor    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│ POST reply to   │────▶│ Print success   │
│ GitHub API      │     │ with URL        │
└─────────────────┘     └────────┬────────┘
                                 │
                        --resolve flag?
                                 │
                    ┌────────────┴────────────┐
                    │                         │
               Yes  ▼                    No   ▼
         ┌─────────────────┐           ┌──────────┐
         │ Resolve thread  │           │  Done    │
         └─────────────────┘           └──────────┘
```

**Examples:**
```bash
# Open editor to compose reply
gh review-responder comment 123456789

# Inline body
gh review-responder comment 123456789 --body "Thanks, fixed!"

# From file
gh review-responder comment 123456789 --body-file response.md

# From pipe
echo "LGTM" | gh review-responder comment 123456789 --stdin

# Reply and resolve
gh review-responder comment 123456789 --body "Done" --resolve
```

---

### resolve Command

Resolves or unresolves review comment threads.

**Usage:**
```bash
gh review-responder resolve [COMMENT_ID]
gh review-responder resolve [PR_NUMBER] [COMMENT_ID]
```

**Flags:**
- `--unresolve` - Mark as unresolved instead
- `--all` - Apply to all unresolved comments
- `--comment <text>` - Add comment when resolving (supports `@file`)
- `--debug` - Enable debug output

#### Batch Resolution Flow

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ Fetch all       │────▶│ Filter          │────▶│ Show summary    │
│ comments        │     │ unresolved      │     │ and confirm     │
└─────────────────┘     └─────────────────┘     └────────┬────────┘
                                                         │
                                                    Confirmed?
                                                         │
                                            ┌────────────┴────────────┐
                                            │                         │
                                       Yes  ▼                    No   ▼
                                 ┌─────────────────┐           ┌──────────┐
                                 │ For each:       │           │ Cancel   │
                                 │ - Add comment   │           └──────────┘
                                 │   (if --comment)│
                                 │ - Resolve thread│
                                 └─────────────────┘
```

**Examples:**
```bash
# Resolve single comment
gh review-responder resolve 123456789

# Unresolve
gh review-responder resolve 123456789 --unresolve

# Resolve with comment
gh review-responder resolve 123456789 --comment "Fixed in abc123"

# Resolve with comment from file
gh review-responder resolve 123456789 --comment @response.md

# Resolve all unresolved comments
gh review-responder resolve --all
```

---

## Package Structure

```
pkg/
├── ai/                    # AI-assisted suggestion application
│   ├── config.go          # Configuration from env/flags
│   ├── gemini.go          # Google Gemini provider
│   ├── prompts.go         # Prompt templates
│   └── provider.go        # Provider interface
│
├── applier/               # Suggestion application logic
│   └── applier.go         # Apply suggestions to files
│
├── diffhunk/              # Diff parsing
│   └── diffhunk.go        # Parse unified diff format
│
├── diffposition/          # Line number mapping
│   └── diffposition.go    # Map between old/new file versions
│
├── github/                # GitHub API client
│   ├── client.go          # GraphQL + REST API calls
│   └── client_test.go     # Tests for URL parsing helpers
│
├── parser/                # Suggestion extraction
│   └── suggestion.go      # Parse ```suggestion blocks
│
└── ui/                    # Terminal UI components
    ├── colors.go          # ANSI colors, markdown rendering
    ├── language.go        # Language detection for syntax
    ├── pr_selector.go     # PR selection widget
    ├── quote.go           # Quote formatting for replies
    ├── selector.go        # Generic TUI selector (types)
    └── selector_impl.go   # Interactive TUI implementation
```

---

## Data Flow

### Comment Data Model

```
┌───────────────────────────────────────────────────────────────────────┐
│                          ReviewComment                                │
├───────────────────────────────────────────────────────────────────────┤
│  ID            int64        # Unique comment ID                       │
│  ThreadID      string       # GraphQL thread ID for mutations         │
│  Author        string       # GitHub username                         │
│  Body          string       # Comment markdown                        │
│  Path          string       # File path                               │
│  Line              int      # Line number (new file)                  │
│  OriginalLine      int      # Line number (old file)                  │
│  OriginalLines     int      # Number of lines in original selection   │
│  StartLine         int      # Multi-line comment start (new)          │
│  EndLine           int      # Multi-line comment end (new)            │
│  OriginalStartLine int      # Multi-line comment start (old)          │
│  OriginalEndLine   int      # Multi-line comment end (old)            │
│  DiffSide          string   # "LEFT" or "RIGHT"                       │
│  SubjectType       string   # "LINE" or "FILE"                        │
│  DiffHunk      string       # Surrounding diff context                │
│  HTMLURL       string       # Web URL to comment                      │
│  CreatedAt     time.Time    # When created                            │
│  IsOutdated    bool         # True if code has changed                │
│  HasSuggestion bool         # Contains suggestion block               │
│  SuggestedCode string       # Extracted suggestion                    │
│  Reactions     Reactions    # Emoji reaction counts                   │
│  ThreadComments []ThreadComment  # Replies in thread                  │
└───────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────┐
│             Reactions               │
├─────────────────────────────────────┤
│  PlusOne    int   # 👍 count        │
│  MinusOne   int   # 👎 count        │
│  Laugh      int   # 😄 count        │
│  Hooray     int   # 🎉 count        │
│  Confused   int   # 😕 count        │
│  Heart      int   # ❤️ count        │
│  Rocket     int   # 🚀 count        │
│  Eyes       int   # 👀 count        │
└─────────────────────────────────────┘
```

### PR Number Detection

When no PR number is given as an argument, the tool auto-detects it:

```
GetCurrentBranchPR()
        │
        ▼
┌─────────────────┐
│ gh pr view      │──── Found? ───▶ Return PR number
│ (current branch)│
└────────┬────────┘
         │ Not found (fork workflow)
         ▼
┌─────────────────┐
│ findForkPR()    │
└────────┬────────┘
         │
         ▼
┌───────────────────────────────────────┐
│ 1. git rev-parse ──▶ current branch   │
│ 2. git branch -r ──▶ find remotes     │
│    with matching branch               │
│ 3. git remote get-url ─▶ extract      │
│    GitHub owner from remote URL       │
│ 4. REST API: /pulls?head=owner:branch │
└───────────────────────────────────────┘
         │
    Found? ────▶ Return PR number
         │
    Not found ─▶ Fall back to interactive PR selector
```

This handles fork-based workflows where `origin` points to the upstream repo
(e.g., `WebKit/WebKit`) but the PR was opened from a personal fork
(e.g., `sideshowbarker/WebKit`). The `gh pr view` command can’t find these
PRs because it only checks the default remote, so the fallback queries the
REST API with the fork-qualified `head=owner:branch` parameter.

### API Call Flow

```
┌─────────────┐                 ┌─────────────┐                 ┌─────────────┐
│   Client    │                 │   gh CLI    │                 │  GitHub API │
└──────┬──────┘                 └──────┬──────┘                 └──────┬──────┘
       │                               │                               │
       │  FetchReviewComments()        │                               │
       │──────────────────────────────▶│                               │
       │                               │  GraphQL: threads query       │
       │                               │──────────────────────────────▶│
       │                               │◀──────────────────────────────│
       │                               │  REST: /pulls/comments        │
       │                               │──────────────────────────────▶│
       │                               │◀──────────────────────────────│
       │◀──────────────────────────────│                               │
       │  []*ReviewComment             │                               │
       │                               │                               │
       │  ResolveThread(threadID)      │                               │
       │──────────────────────────────▶│                               │
       │                               │  GraphQL: resolveReviewThread │
       │                               │──────────────────────────────▶│
       │                               │◀──────────────────────────────│
       │◀──────────────────────────────│                               │
       │                               │                               │
```

---

## Performance Optimizations

### Cached Comment Data

All comment data is fetched once at startup. Subsequent operations use cached data:

- Viewing detail view: No API call
- Opening in browser: Uses cached URL
- Thread comment selection: Uses cached thread data
- Only mutations and explicit refresh (`i`) make API calls

### Cached Markdown Renderer

```go
var cachedMarkdownRenderer *glamour.TermRenderer
var rendererInitOnce sync.Once

func getMarkdownRenderer() *glamour.TermRenderer {
    rendererInitOnce.Do(func() {
        cachedMarkdownRenderer, _ = glamour.NewTermRenderer(
            glamour.WithStandardStyle("dark"),
            glamour.WithWordWrap(80),
        )
    })
    return cachedMarkdownRenderer
}
```

### Markdown Warmup

The first markdown render can be slow due to chroma lexer initialization. We warm up in the background at startup:

```go
func WarmupMarkdownRenderer() {
    go func() {
        r := getMarkdownRenderer()
        if r != nil {
            r.Render("```go\nfunc main() {}\n```")
            r.Render("```js\nconst x = 1;\n```")
        }
    }()
}
```

### Pre-compiled Regexes

Regular expressions are compiled once at package init time:

```go
var (
    suggestionBlockRe = regexp.MustCompile("(?s)```suggestion\\s*\\n.*?```")
    imageMarkdownRe   = regexp.MustCompile(`!\[.*?\]\(.*?\)`)
    diffHeaderRe      = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)
)
```

### Thread-safe Debug Flag

The `uiDebug` flag uses atomic operations for thread-safe access from background goroutines:

```go
var uiDebug atomic.Bool

func SetUIDebug(enabled bool) {
    uiDebug.Store(enabled)
}
```

---

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `EDITOR` | Editor for composing replies | `vim` |
| `GH_REVIEW_RESPONDER_AGENT` | Coding agent command | `claude` |
| `GH_REVIEW_CONDUCTOR_AGENT` | Coding agent command, honored after `GH_REVIEW_RESPONDER_AGENT` for setups predating the rename | `claude` |
| `GEMINI_API_KEY` | Gemini AI API key | - |
| `OPENAI_API_KEY` | OpenAI API key | - |
| `ANTHROPIC_API_KEY` | Claude API key | - |
| `NO_COLOR` | Disable colored output | - |

---

## Error Handling

### Diagnostic Files

When suggestion application fails, diagnostic files are written to `/tmp/`:

- `gh-review-responder-mismatch-*.diff` - Expected vs actual content
- `gh-review-responder-patch-*.patch` - Failed patch with error details
- `gh-review-responder-ai-patch-*.patch` - Failed AI-generated patch

### Debug Mode

All commands support `--debug` for detailed output:

```bash
gh review-responder browse 123 --debug
gh review-responder apply 123 --debug
```

This logs:
- API request/response details
- Timing information
- Position mapping calculations
- Patch generation steps
