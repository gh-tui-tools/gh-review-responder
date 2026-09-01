# gh-review-responder

A GitHub CLI extension that applies review comments and suggestions directly to
your local checkout.

<img width="1351" height="500" alt="image" src="https://github.com/user-attachments/assets/1755f11a-9321-49ee-a13e-786a58b2d78e" />

## Overview

Use `gh review-responder` to pull review comments, preview suggested changes, and apply
them interactively or in bulk, all from the terminal.

## Installation

```bash
gh extension install gh-tui-tools/gh-review-responder
```

Or build from source:

```bash
git clone https://github.com/gh-tui-tools/gh-review-responder
cd gh-review-responder
go build
gh extension install .
```

## Commands

### Color control

Pass `--no-color` or set `NO_COLOR=1` to disable ANSI colors, emojis, and OSC8 hyperlinks in all output (including interactive views).

### List

Fetch unresolved comments for the current PR (or pass `[PR_NUMBER] [THREAD_ID]`).

```bash
gh review-responder list [PR_NUMBER] [THREAD_ID]
gh review-responder list --all
gh review-responder list --json
```

### Apply

Preview and apply suggestions interactively, or add `--all`, `--file`, or
`--include-resolved` for batch updates. `--debug` prints verbose logs and AI flags
(--ai-auto, --ai-provider, --ai-model, --ai-template, --ai-token) help with
conflicting cases.

```bash
gh review-responder apply [PR_NUMBER]
gh review-responder apply --all [PR_NUMBER]
```

**Tip:** keep a clean working tree before running apply.

### Browse

Navigate review comments in an interactive selector, jump to a specific comment,
or open it in your browser.

```bash
gh review-responder browse
gh review-responder browse <COMMENT_ID>
```

### Resolve

Resolve or unresolve threads, add comments, or resolve all for the current PR.

```bash
gh review-responder resolve [COMMENT_ID]
gh review-responder resolve --all
```

### Comment

Reply via editor, inline `--body`, file, or stdin input. Use `--resolve` to mark
threads resolved after replying.

```bash
gh review-responder comment <COMMENT_ID> [PR_NUMBER]
```

## Features

- fetches GitHub review comments and parses suggestion blocks
- previews and applies suggestions with an interactive UI
- browse comments, resolve threads, and reply without leaving the terminal
- optional AI-assisted application for fuzzy or outdated suggestion hunks

## Requirements

- GitHub CLI (`gh`) installed and authenticated
- Git repository with a GitHub remote and an active PR

## License

[Apache License 2.0](./LICENSE)
