# Contributing to matrixOS

Thank you for your interest in contributing! matrixOS is a hobby project — contributions are welcome, but please keep them focused and small.

## What you can work on without a full Gentoo build environment

The following areas do not require a Gentoo host with root, OSTree, or significant disk and RAM:

- The `vector` CLI tool (`vector/` — requires Go to be installed)
- Shell build scripts and their BATS tests (`dev/`)
- Documentation (`README.md`, `SECURITY.md`, etc.)
- CI/CD workflows (`.github/workflows/`)

Changes to the build layers (`build/seeders/`), release hooks (`release/`), or image creation (`image/`) require a Gentoo host with root, OSTree, and significant disk and RAM. Please discuss these in an issue before starting work.

## Getting started

**1. Fork the repository** and clone your fork locally.

**2. Build the vector CLI:**

```bash
cd vector
go build ./...
```

**3. Run all tests** (Go unit tests + BATS shell tests):

```bash
./dev/test.sh
```

`go` is required to build and test the vector CLI (steps 2 and 4). `bats` is needed for the shell tests but optional — `./dev/test.sh` skips them gracefully if `bats` is not installed.

**4. Run Go checks:**

```bash
cd vector
go vet ./...
gofmt -l .      # no output means clean
```

## Submitting a pull request

- **Open an issue first** for anything beyond small docs or test fixes — it avoids duplicated effort.
- **One PR per concern** — do not bundle unrelated changes.
- Ensure `go vet ./...` and `gofmt -l .` produce no output before submitting.
- Ensure `./dev/test.sh` passes before submitting.
- Write a clear commit message explaining *why*, not just *what*.
- **Reply to and resolve all review feedback** on your PR. PRs with unaddressed comments will not be merged.

## AI-assisted contributions

AI tools (such as Claude, Copilot, etc.) are welcome to use when preparing contributions. The submitting contributor is responsible for:

- Reviewing and understanding all AI-generated output before opening a PR
- Testing the changes locally (`./dev/test.sh`, `go vet`, `gofmt`)
- Ensuring all contributions are submitted under the project's existing [BSD 2-Clause licence](LICENSE)

The AI is a tool — the contributor is accountable for the quality of what is submitted.

> **Warning:** Do not open a PR without reviewing, understanding, and testing all the changes it contains — AI-generated or otherwise. Pull requests that appear to contain unreviewed AI-generated content will be deprioritised and may be closed without review. PR authors are responsible for actively responding to and resolving all review feedback on their own PRs.

## Licence

By contributing, you agree that your contributions will be licensed under the [BSD 2-Clause "Simplified" Licence](LICENSE).
