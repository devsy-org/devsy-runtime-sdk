# Devsy Runtime SDK

This Go module defines Runtime Protocol v1. Hosts launch runtime processes through [go-plugin](https://github.com/hashicorp/go-plugin) and call them over gRPC. Runtimes manage workspace resources and execute commands. Devsy handles images, Dev Containers, agents, and workspace state.

## Install

Use Go 1.26+:

```sh
go get github.com/devsy-org/devsy-runtime-sdk
```

The module includes protobuf bindings. Consumers do not need generation tools.

## Implement a runtime

Implement `runtimev1.RuntimeDriverServer`, embed `UnimplementedRuntimeDriverServer` by value, and call `server.Serve` from `main`. Implement `Info`, lifecycle operations, and `Exec`. Report capabilities through `Info`. Use `runtimev1.ValidateInfo` before a host invokes runtime operations.

Hosts use `plugin.Handshake()`, `plugin.ProtocolVersion`, and `plugin.ClientPlugins()` with `go-plugin.ProtocolGRPC`. The host resolves the executable, sets a startup timeout, and calls `Client.Kill()` during cleanup. The handshake does not authenticate or sandbox the runtime.

`Exec` frames carry stdout and stderr bytes. An exit frame ends the stream. Cancellation must stop the command and release its resources. Send diagnostics to stderr.

API 1.1 separates `remote_user`, the workspace owner, from `user`, the process user. API 1.2 adds `ReusePreflight` for runtimes that advertise `reuse_preflight`. API 1.3 permits `RECREATE_MODE_ON_RUN` when the host opts in through `InfoRequest.supports_recreate_on_run`. Replacement requires `RunImageRequest.allow_recreate=true` on that request. Validate the configuration before removing a workspace. Without permission, a duplicate must return `AlreadyExists` and leave the workspace intact.

See [runtime.proto](proto/devsy/runtime/v1/runtime.proto) for the RPC schema.

## Test and develop

`conformance.Run` checks a runtime through its client transport. The `cmd/devsy-fake-runtime` fixture supports integration tests. The `supervisor` package owns a runtime process tree through a go-plugin runner. It is not a sandbox; hosts must verify executable paths.

```sh
mise install
mise exec -- prek run --all-files
mise exec -- go test -race ./...
mise exec -- go vet ./...
```

After schema changes, run `mise exec -- go generate ./...` and check for generation drift.
