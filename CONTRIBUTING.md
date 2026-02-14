# Contributing to ralphex

## Development Setup

1. Clone the repository
2. Install Go 1.26+
3. Run `make test` to verify setup

## Code Style

- Follow standard Go conventions
- All comments lowercase except godoc
- Use table-driven tests with testify
- Aim for 80%+ test coverage

## Pull Requests

1. Create a feature branch from master
2. Make your changes with tests
3. Run `make test lint` before submitting
4. Submit PR with clear description

## AI-Assisted Contributions

AI-assisted development is welcome - this project is designed for that workflow. However, we have specific expectations for such PRs:

**Quality expectations:**

- Contributors must review their own code before submitting, whether written by AI or not
- PRs must follow project conventions. Explore the codebase first (with or without AI assistance) to understand patterns. Examples include:
  - Code organization: flat package structure, one `_test.go` file per source file
  - Testing: table-driven tests with testify, moq-generated mocks, 80%+ coverage
  - Error handling: wrap with context using `fmt.Errorf("context: %w", err)`
  - Library usage: use existing libraries, don't add new dependencies without discussion
  - Interfaces: define at the consumer side, not the provider
- Code must be readable and understandable by humans
- Commit messages and PR descriptions should be meaningful, not generic AI output
- Keep PRs focused: "general improvements" to unrelated code don't belong in the same PR unless directly warranted by the feature being implemented. Submit unrelated improvements as separate PRs
- Contributors are responsible for checking AI output for security issues - AI tools can introduce vulnerabilities that aren't obvious at first glance
- You must understand and be able to explain every line of code you submit. If asked about your changes during review, "the AI wrote it" is not an acceptable answer

**Reviewable scope:**

- PRs must be reasonably sized for human review
- Large changes should be split into focused, logical PRs
- A PR touching dozens of files with thousands of lines is not reviewable - break it down

**What we will not accept:**

- Unreviewed AI output dumped for maintainers to fix
- Code without tests or with failing tests/linter
- Changes that ignore project conventions after being pointed to them
- PRs that don't respond to review feedback

PRs that violate these guidelines may be closed without further discussion. We value contributions but cannot serve as free QA for bulk AI-generated code.

## Reporting Issues

Please include:
- Go version
- OS and architecture
- Steps to reproduce
- Expected vs actual behavior
