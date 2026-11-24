# Agent Guidelines for matrixOS

This document defines the engineering, architecture, and verification standards that AI coding agents must follow when working in this repository. See also [README.md](README.md), [build/README.md](build/README.md), and [CONTRIBUTING.md](CONTRIBUTING.md).

## 1. Repository Layout & Boundaries

matrixOS is a Gentoo-based Linux distribution using OSTree for atomic upgrades. The build pipeline runs in three stages: **Seeder -> Releaser -> Imager**, orchestrated by the Go CLI in `vector/`.

- `vector/` — Go CLI (`vector/main.go`, `vector/commands/`, `vector/lib/`). User-facing commands (`upgrade`, `flash`, `setupOS`, `readwrite`, `jailbreak`, `branch`, `kargs`, `cfg`) and build/dev commands (`build seeds|release|releases|image|images|all`, `dev enter|janitor|check|vm`).
- `build/` — Seeder layer definitions (`build/seeders/00-bedrock`, `10-server`, `20-gnome`, `21-cosmic`), shared shell libraries (`build/seeders/lib/{preppers_lib.sh,chroots_lib.sh}`), and the unshare/chroot PID-1 entrypoint (`build/init/init.sh`).
- `release/` — Releaser hooks (`release/hooks/`) and service configs (`release/services/`), following the `OSNAME/ARCH/SEEDER_NAME.{sh,conf}` convention.
- `image/` — Imager hooks (`image/hooks/`) for partitioning, bootloaders, and image creation, plus VM test definitions.
- `conf/` — INI configuration files: `conf/matrixos.conf` (build/base config) and `conf/client.conf` (deployed-system overlay with `ParentConfig=matrixos.conf`), plus `*.conf.d/*.conf` drop-in overrides. Toolkit root discovery walks up from CWD looking for the `.matrixos` marker file (falling back to `/etc/matrixos/conf` when `config.SystemSearchPathEnabled` is true).
- `dev/` — Developer wrapper scripts (`dev/build.sh`, `dev/test.sh`, `dev/cdn_pusher.example.sh`) and their BATS tests.
- `pubkeys/` — Public GPG and SecureBoot keys.
- **Gitignored / host-state directories (NEVER commit, expose, or modify secrets/build outputs here)**:
  - `private/` — Local private signing keys repo (GPG, SecureBoot). Never read, print, copy, or commit anything under `private/`.
  - `out/`, `ostree/`, `locks/`, `logs/`, `conf/*.conf.d/*.conf` — Local build artifacts, OSTree repos, lock files, logs, and local config overrides.

## 2. Go Toolchain & Language Compatibility

- `go.mod` and `.github/workflows/release-vendor.yml` pin **`go 1.27.0`**.
- Even if the local host runs a newer Go compiler, **never** use language features or stdlib APIs introduced after the Go version pinned in `go.mod` (`go 1.27.0`), which fail `go vet` / builds targeting `go 1.27.0`.
- Keep dependencies minimal (`go.mod` currently depends only on `golang.org/x/sys`). Do not add third-party dependencies without explicit user approval.

## 3. matrixOS Go Architecture & Invariants

### Configuration (`vector/lib/config`)
- `IniConfig` loads base config + optional `ParentConfig` + `*.conf.d/*.conf`, storing per-key value history (`map[string][]string`) and expanding relative paths against `matrixOS.Root`, `matrixOS.DefaultRoot`, `matrixOS.ConfRoot`, `matrixOS.ArtifactsRoot`, and `matrixOS.PrivateGitRepoPath`.
- **`IniConfig.GetBool` vs `MockConfig.GetBool` quirk**: `IniConfig.GetBool(k)` checks `GetItem(k) == "true"`, whereas `MockConfig.GetBool(k)` reads `m.Bools[k]` (separate from `m.Items[k]`). Before switching any caller between `GetItem(k) == "true"` and `GetBool(k)`, audit all tests using `MockConfig` so mock fixtures populate the matching field.
- Always isolate per-worker configuration overrides by calling `cfg.Clone()` + `AddOverlay(...)` rather than mutating a shared `IConfig`.

### Parallelism, Worker Isolation & Shared Resources
- **Per-worker instances**: Parallel stages (`seeder`, `releaser`, `imager`) run multiple goroutines concurrently. Never share mutable `*ostree.Ostree`, `*filesystems.Fsenc`, `*imager.Imager`, `*releaser.Releaser`, or `*seeder.Seeder` state across workers without explicit synchronization; construct isolated per-worker instances (`ostree.NewOstree`, cloned config, per-worker buffered writers).
- **Shared host/repo serialization**:
  - **GPG keyring/agent**: `ostree.GpgKeyID`, `ostree.GpgSignFile`, and `ostree.PatchGpgHomeDir` mutate shared GPG home dirs (`gpg-agent.conf`, keyrings) and must stay serialized under `ostree.gpgMu`.
  - **OSTree repository mutations**: Concurrent `ostree` repo operations (`InitRepo`, `MaybeInitializeRemote`, `Commit`, `Prune`, `GenerateStaticDelta`, `UpdateSummary`) targeting the same `repoDir` must be serialized per canonical repo path via `ostree.LockRepo` to avoid lock contention, TOCTOU races, and summary corruption.
  - **Host-side LUKS device-mapper names**: In `ParallelImage`, each worker must receive a unique host-side `Imager.EncryptedRootFsName` so concurrent `cryptsetup open` / `Cleanup` calls never collide on `/dev/mapper/<name>`. Note that guest kernel boot args use `rd.luks.uuid=<UUID>` (`imager/bootloader.go`), not the host mapper name.
  - **Loop devices**: `filesystems.Loop.Attach()` serializes `LOOP_CTL_GET_FREE` -> `LOOP_SET_FD` with `attachMu` and retries on cross-process `EBUSY`. Always read device paths through synchronized accessors rather than racing on struct fields.
  - **Signal handling**: `commands.SignalGuard` coordinates nested guards via a process-wide LIFO stack (`signalMu` / `signalStack`) so inner guards (e.g. `all` calling `seeds`/`releases`/`images`) never clobber outer signal handlers or leak cleanups. Preserve `Disarm()` no-op semantics on unarmed guards.
  - **Cgroup v2 worker pools**: `cgroups.WorkerPool` enforces memory + CPU (`cpuset` or `cpu.max`) isolation for parallel seeders via `clone3(CLONE_INTO_CGROUP)` (`SysProcAttr`). Parent cgroup directories must be unique per pool instance within a process, and `Close()` must synchronize cleanly against concurrent `BoostWorker`/`UnboostWorker`/`SysProcAttr`.

### Safety, Testing & Dependency Injection Conventions
- **Rootfs safety**: Always guard destructive operations (`rm -rf`, mounts, chroot staging) with `filesystems.CheckDirIsRoot` or path validation so an empty/misconfigured path can never target `/`.
- **Command execution**: Inject `runner.Func` / `runner.ChrootRunFunc` (defaulting to `runner.Run` / `runner.ChrootRun`) instead of hardcoding `exec.Command` directly in library packages, so unit tests can mock subprocesses without root or external binaries.
- **Output writers**: Route library output through injected `io.Writer`s (`stdout`/`stderr`, synchronized for concurrent use) rather than writing directly to `os.Stdout`/`os.Stderr`, so parallel workers can buffer and prefix logs cleanly and commands can mirror output to log files.
- **Interface conformance**: Keep compile-time interface assertions (`var _ IFoo = (*Foo)(nil)` and mock checks) on library implementations and mocks.
- **Lifecycle cleanups in commands**: Unit tests may invoke internal command methods (e.g. `EnterCommand.run()`) directly without going through `RunWithGuard`; verify test call paths before removing local `defer Cleanup()` calls.

## 4. Shell & BATS Conventions

- All bash scripts (`build/`, `release/`, `image/`, `dev/`) must use `set -eu` and quote variable expansions (`"${var}"`).
- `build/init/init.sh` runs as PID 1 inside `unshare` namespaces and bind-mounts host caches (`SEEDER_DISTFILES_DIR`, `SEEDER_BINPKGS_DIR`, `SEEDER_PRIVATE_GIT_REPO_PATH`, `IMAGER_EFI_MOUNT`, `IMAGER_BOOT_MOUNT`) with `--make-private` before `exec`ing into the target chroot script. Keep its env var contract in sync with `vector/lib/runner/` and `vector/lib/{seeder,releaser,imager}/`.
- Shell tests use BATS (`*_test.bats` under `build/`, `vector/`, and `dev/`) and are discovered and executed by `./dev/test.sh`.

## 5. Never Rely on the User to Catch Your Bugs

Do not treat the user as a second-pass reviewer for correctness, deadlocks, or missing edge cases. Perform a full adversarial self-audit of your own diff **before** reporting completion.

If an audit or task identifies multiple issues in scope, fix all of them (or explicitly confirm scope before stopping) rather than silently omitting harder fixes.

## 6. Reproduce-First Rule for Bugs and Race Conditions

- **Prove the bug exists before fixing it**: Write a minimal, deterministic unit test that reproduces the bug or race condition and confirm it **fails** (under `go test -race` for concurrency issues) against the unmodified code. Do not speculate or claim fixes for unverified issues.
- **Keep the reproducer as a regression test**: After applying the fix, re-run the test (e.g. `go test -race -count=3`) to confirm it reliably passes.

## 7. Mandatory Pre-Completion Self-Audit Checklist

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
   - **Edge cases**: Check nil receivers/options, empty slices/maps, zero/negative timeouts, `Parallelism <= 0`, idempotent `Close()`/`Disarm()`/`Cleanup()`, and LIFO `defer` ordering (e.g. flush writers before closing log files; cancel contexts before waiting on child processes).
4. **Formatting & static checks**:
   - Run `gofmt -l vector/` (must be empty on all modified files; keep formatting clean).
   - Run `go vet ./vector/...` (must report zero warnings, including `copylocks`).

## 8. Required Verification Commands & Known Test Caveats

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

**Known host-environment test caveats:**
- Root-only tests (such as loop/mount integration tests) skip automatically when `os.Getuid() != 0`.
- `TestAddRemoteToRootfs` in `vector/lib/ostree/repo_test.go` checks for `"/sysroot"` on the host filesystem and fails on non-matrixOS hosts where `/sysroot` does not exist.
- Report exact test outcomes transparently, including any pre-existing host-environment failures (never hide or gloss over failures).

## 9. Git & Commit Hygiene

- Keep commits small, focused, and logically stacked (one concern per commit) with commit messages explaining *why*, not just *what*.
- Do **not** run `git push` or rewrite published history unless explicitly instructed by the user.
