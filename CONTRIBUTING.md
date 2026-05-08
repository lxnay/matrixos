# Contributing to matrixOS

Thank you for your interest in contributing! matrixOS is a hobby project — contributions are welcome, but please keep them focused and small.

## What you can work on without a Gentoo host

The following areas do not require a full Gentoo build environment and are the most accessible for new contributors:

- The `vector` CLI tool (`vector/` — Go)
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

This requires `go` and `bats` to be installed. Each tool skips gracefully if not found.

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

## AI-assisted contributions

AI tools (such as Claude, Copilot, etc.) are welcome to use when preparing contributions. The submitting contributor is responsible for:

- Reviewing and understanding all AI-generated output before opening a PR
- Testing the changes locally (`./dev/test.sh`, `go vet`, `gofmt`)
- Ensuring all contributions are submitted under the project's existing [BSD 2-Clause licence](LICENSE)

The AI is a tool — the contributor is accountable for the quality of what is submitted.

## Licence

By contributing, you agree that your contributions will be licensed under the [BSD 2-Clause "Simplified" Licence](LICENSE).
