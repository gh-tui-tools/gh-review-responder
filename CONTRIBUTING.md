# Contributing to gh-review-responder

Thank you for your interest in contributing to gh-review-responder! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.21 or higher
- GitHub CLI (`gh`) installed and authenticated
- Git

### Getting Started

1. Fork and clone the repository:

```bash
git clone https://github.com/gh-tui-tools/gh-review-responder.git
cd gh-review-responder
```
2. Install commit hooks:

   The repository contains a file called `.pre-commit-config.yaml` that defines “commit hook” behavior to be run locally in your environment each time you commit a change to the sources. To enable that “commit hook” behavior, first follow the installation instructions at https://pre-commit.com/#install, and then run this:

   ```bash
   pre-commit install
   ```

3. Install dependencies:

```bash
make deps
```

4. Build the project:

```bash
make build
```

5. Run tests:

```bash
make test
```

## Development Workflow

### Building

```bash
make build
```

This will create the `gh-review-responder` binary in the project root.

### Testing Locally

To test the plugin locally without installing it:

```bash
./gh-review-responder list
./gh-review-responder apply
```

Or install it as a local extension:

```bash
make install
gh review-responder list
```

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage
```

### Code Quality

```bash
# Format code
make fmt

# Run linter
make lint
```

## Making Changes

### Branch Naming

- Feature: `feature/description`
- Bug fix: `fix/description`
- Documentation: `docs/description`

### Commit Messages

Follow conventional commits:

- `feat: add new feature`
- `fix: resolve bug`
- `docs: update documentation`
- `test: add tests`
- `refactor: improve code structure`

### Pull Request Process

1. Create a new branch for your changes
2. Make your changes with clear, descriptive commits
3. Add or update tests as needed
4. Ensure all tests pass: `make test`
5. Format your code: `make fmt`
6. Push your branch and create a pull request
7. Describe your changes in the PR description
8. Wait for review and address any feedback

## Project Structure

```
.
├── cmd/                    # Command implementations
│   ├── root.go            # Root command
│   ├── list.go            # List command
│   └── apply.go           # Apply command
├── pkg/                   # Packages
│   ├── github/            # GitHub API client
│   ├── parser/            # Suggestion parser
│   └── applier/           # File applier
├── main.go                # Entry point
├── go.mod                 # Go module file
├── Makefile               # Build automation
└── README.md              # Documentation
```

## Adding New Features

When adding new features:

1. Discuss the feature in an issue first
2. Update relevant documentation
3. Add tests for new functionality
4. Ensure backward compatibility
5. Update the README if needed

## Testing Guidelines

- Write tests for all new functionality
- Aim for good test coverage
- Use table-driven tests where appropriate
- Test edge cases and error conditions

### Coverage Reporting

The project uses build tags to exclude interactive TUI code from coverage metrics.
Code in `*_nocov.go` files (with `//go:build !coverage`) contains untestable
interactive terminal code. The `make test-coverage` command uses `-tags=coverage`
to swap in stub implementations, giving accurate coverage for testable code.

Example test structure:

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        // Test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test logic
        })
    }
}
```

## Reporting Issues

When reporting issues:

1. Use the issue tracker
2. Provide a clear title and description
3. Include steps to reproduce
4. Specify your environment (OS, Go version, gh version)
5. Include relevant logs or error messages

## Questions?

Feel free to open an issue for any questions or discussions about contributing.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
