# AGENTS.md

## Environment and commands

This repository contains Devsy's Runtime Protocol v1, generated Go bindings,
plugin transport helpers, conformance tests, and runtime process fixtures.
Use the toolchain pinned in `mise.toml`; install it with `mise install`.
Consumers use the checked-in bindings without installing generation tools.

Run commands through `mise exec --`:

- `go test -race ./...`: unit, conformance, and process-ownership tests.
- `go vet ./...`: Go static checks.
- `golangci-lint run`: strict Go linting.
- `golangci-lint fmt --diff`: verify formatting; omit `--diff` to apply it.
- `prek run --all-files`: all `prek.toml` hooks.
- `buf lint` and `buf format --diff --exit-code`: protobuf checks.
- `go generate ./...`: regenerate checked-in protobuf/gRPC bindings.

After changing the schema or generation tooling, regenerate and verify that a
second generation produces no diff. Never edit generated bindings by hand.

## Go design and protocol contracts

Follow idiomatic Go and the [Uber Go style guide](https://github.com/uber-go/guide/blob/master/style.md).
Prefer simple functions, small interfaces at actual capability boundaries,
explicit errors, and clear ownership of subprocesses, streams, and cancellation.
Refactor only for a concrete correctness, clarity, or maintenance benefit.
Prefer `testify/suite` where it fits existing tests. Keep comments for exported
contracts and non-obvious invariants rather than restating implementation.

- Keep this SDK independent of Devsy core and runtime-specific backend code.
- Protocol changes require consideration of host callers, runtime servers,
  capability negotiation, typed errors, and conformance fixtures together.
- Preserve binary stream fidelity, distinct stdout/stderr, bounded buffering,
  stdin half-close, authoritative exit status, and deterministic cleanup.
- A successful terminal exit must not conceal a backend or transport error.
- Preserve process ownership and cleanup on Linux, macOS, and Windows. Prefer
  deterministic synchronization over scheduler-sensitive sleeps in tests.
- Treat plugin configuration as trusted executable configuration; a handshake
  cookie identifies the protocol and is not authentication.
- Keep README documentation standalone. Public Devsy documentation belongs in
  the main Devsy repository; do not reference internal planning documents.

## Validation and pull requests

Start from current `origin/main`, inspect the complete relevant diff and callers,
and preserve unrelated work. Run lint, formatting, pre-commit, and relevant tests
before pushing. Schema changes also require protobuf and generation checks.
Cross-platform CI and process probes must pass when applicable.

Use scoped branches, signed Conventional Commits, and concise subjects. Satisfy
any repository CLA requirements. Open draft PRs for review. Verify validation,
applicable CI checks, and completed Greptile and CodeRabbit reviews against the
final head; resolve significant valid findings before merging. A skipped or
rate-limited review is pending, not a completed review. Merge only with human
authorization covering the change.

**Merged commits must contain only a single-line Conventional Commit subject,
with an empty body.** When authorized to merge, squash with an explicit subject
and an explicitly empty body; never copy the PR description or commit list into
the merged commit. Apply the same rule to automated release PR merges and verify
the resulting commit message.
