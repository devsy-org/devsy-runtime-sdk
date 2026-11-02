# Devsy Runtime SDK

The SDK defines Runtime Protocol v1. Hosts launch runtimes through [go-plugin](https://github.com/hashicorp/go-plugin) over gRPC. Runtimes manage workspaces and run commands.

## Install

Go 1.26+:

```sh
go get github.com/devsy-org/devsy-runtime-sdk
```

## Implement a runtime

Implement `runtimev1.RuntimeDriverServer`, embedding `UnimplementedRuntimeDriverServer` by value. Call `server.Serve` from `main`. Implement `Info`, lifecycle RPCs, `Exec`, and `Logs` when `Info.Capabilities.logs` is true. Hosts call `runtimev1.ValidateInfo`.

Hosts configure `plugin.Handshake()`, `plugin.ProtocolVersion`, and `plugin.ClientPlugins()` under `go-plugin.ProtocolGRPC`, then call `Client.Kill()`. The handshake is not authentication or sandboxing.

`Exec` frames carry stdout and stderr bytes, followed by one exit frame. Reject output after exit. See [Exec conformance](conformance/exec.go) for chunk limits and exit rules. Cancel commands and release resources; send diagnostics to stderr.

API 1.1: `remote_user` owns workspaces; `user` selects the process user. API 1.2: `ReusePreflight` requires `reuse_preflight`. API 1.3: `RECREATE_MODE_ON_RUN` requires `InfoRequest.supports_recreate_on_run`. Only `RunImageRequest.allow_recreate=true` authorizes replacement; validate before removal. Otherwise duplicates return `AlreadyExists` without mutation.

[runtime.proto](proto/devsy/runtime/v1/runtime.proto) specifies RPCs.

## Test and develop

Pass `Connect`, `Workspace`, and `Command` to `conformance.Run`. `Command` returns `*runtimev1.ExecStart` with matching `workspace_id`, `tty=false`, and nonempty argv. Map scenarios:

- `Echo`: stdin to stdout until `CloseStdin`; exit 0.
- `Arguments`: each `ArgumentValues()` value then NUL on stdout; exit 0.
- `Shell`: `shell stdout\n` on stdout; exit 0.
- `Stderr`: stdin to stderr; exit 0.
- `Duplex`: stdin to stdout and stderr; exit 0.
- `Nonzero`: `diagnostic` on stderr; nonzero exit.
- `Signal`: report a signal.
- `EarlyExit`: exit 0 without reading stdin.
- `Block`: `ready\n` on stdout; wait for cancellation.

Use `cmd/devsy-fake-runtime` for integration tests. `supervisor` is not a sandbox. Verify executable paths. On Unix, children that leave the process group need another owner.

```sh
mise install
mise exec -- prek run --all-files
mise exec -- go test -race ./...
mise exec -- go vet ./...
```

Schema changes: `mise exec -- go generate ./...`; check for drift.
