# Agent Guidelines for matrixOS

This document defines the engineering and verification standards that AI coding agents must follow when working in this repository. See also [CONTRIBUTING.md](CONTRIBUTING.md).

## 1. Never Rely on the User to Catch Your Bugs

Do not treat the user as a second-pass reviewer for correctness, deadlocks, or missing edge cases. Perform a full adversarial self-audit of your own diff **before** reporting completion.

If an audit or task identifies multiple issues in scope, fix all of them (or explicitly confirm scope before stopping) rather than silently omitting harder fixes.

## 2. Reproduce-First Rule for Bugs and Race Conditions

- **Prove the bug exists before fixing it**: Write a minimal, deterministic unit test that reproduces the bug or race condition and confirm it **fails** (under `go test -race` for concurrency issues) against the unmodified code. Do not speculate or claim fixes for unverified issues.
- **Keep the reproducer as a regression test**: After applying the fix, re-run the test (e.g. `go test -race -count=3`) to confirm it reliably passes.

## 3. Mandatory Pre-Completion Self-Audit Checklist

Before declaring any change done or committing it, review your own diff line by line for:

1. **Lock ordering & re-entrancy (deadlock check)**:
   - Trace every lock acquisition (`sync.Mutex`, `sync.RWMutex`, per-resource locks) across all callers and callees.
   - Verify no lock can be re-entered on the same goroutine and no two locks can be acquired in opposite orders.
   - Ensure mutexes are never held across arbitrary user callbacks or blocking I/O unless explicitly required and verified cycle-free.
2. **Channel / pipe backpressure & goroutine lifecycle**:
   - Never feed an OS pipe (`cmd.StdoutPipe()`, `io.Pipe()`, etc.) into a bounded channel whose consumer can stop reading early (e.g. on timeout or before `cmd.Wait()`). Ensure readers drain continuously and goroutines always terminate without leaking.
3. **Subtle behavioral & OS-level regressions**:
   - **File permissions**: Replacing `os.Create` (`0666 &^ umask`) with `os.CreateTemp` silently changes permissions to `0600`. Always preserve or explicitly set file modes before atomic rename.
   - **Call-site assumptions**: Grep all callers of any modified helper before changing signatures, return semantics, error messages, or side effects.
   - **Edge cases**: Check nil receivers/options, empty slices/maps, zero/negative timeouts, idempotent `Close()`/`Disarm()`/`Cleanup()`, and LIFO `defer` ordering.
4. **Formatting & static checks**:
   - Run `gofmt -l vector/` (must be empty).
   - Run `go vet ./vector/...` (must report zero warnings, including `copylocks`).

## 4. Required Verification Commands

Run all applicable checks after your final edit:

```bash
go build ./vector/...
go vet ./vector/...
gofmt -l vector/
go test -race ./vector/...
./dev/test.sh
```

For concurrency or race fixes, stress-test the affected packages with `-count=3` (or higher):

```bash
go test -race -count=3 ./vector/<pkg>
```

Report exact test outcomes transparently, including any pre-existing host-environment failures (never hide or gloss over failures).

## 5. Git & Commit Hygiene

- Keep commits small, focused, and logically stacked (one concern per commit) with commit messages explaining *why*, not just *what*.
- Do **not** run `git push` or rewrite published history unless explicitly instructed by the user.
