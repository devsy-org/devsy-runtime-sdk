# Devsy Runtime SDK

The SDK defines Runtime Protocol v1. Hosts launch runtimes through [go-plugin](https://github.com/hashicorp/go-plugin) over gRPC. Runtimes manage workspaces and run commands.

## Install

Go 1.26+:

```sh
go get github.com/devsy-org/devsy-runtime-sdk
```

The module includes protobuf bindings.

## Implement a runtime

Implement `runtimev1.RuntimeDriverServer` and embed `UnimplementedRuntimeDriverServer` by value. Call `server.Serve` from `main`; implement `Info`, lifecycle RPCs, and `Exec`. Hosts call `runtimev1.ValidateInfo` before operations.

Hosts configure `plugin.Handshake()`, `plugin.ProtocolVersion`, and `plugin.ClientPlugins()` with `go-plugin.ProtocolGRPC`, then call `Client.Kill()` during cleanup. The handshake does not authenticate or sandbox runtimes.

`Exec` frames carry stdout and stderr bytes and end with an exit frame. Cancellation must stop commands and release resources. Send diagnostics to stderr.

API 1.1: `remote_user` owns the workspace; `user` selects the process user. API 1.2: `ReusePreflight` requires `reuse_preflight`. API 1.3: `RECREATE_MODE_ON_RUN` requires host opt-in through `InfoRequest.supports_recreate_on_run`. Replace only when `RunImageRequest.allow_recreate=true`; validate before removal. Without permission, duplicates return `AlreadyExists` without mutation.

[runtime.proto](proto/devsy/runtime/v1/runtime.proto) defines the RPC schema.

## Test and develop

Pass `Connect`, `Workspace`, and `Command` to `conformance.Run`. Map `Command` to argv with `tty=false`:

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

After schema changes, run `mise exec -- go generate ./...` and check for drift.
