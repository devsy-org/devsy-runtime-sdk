# Devsy Runtime SDK

Runtime Protocol v1 and shared gRPC serving tools for trusted external Devsy runtime drivers.

This is Workstream B1 of the runtime campaign. The SDK is independent of the Devsy Go module. Devsy retains image preparation, Dev Container orchestration, agent delivery, and workspace state.

## Packages

- `runtimev1`: generated protobuf/gRPC bindings, API versions, and Info validation.
- `plugin`: shared handshake, plugin name, and gRPC registration/client bridge.
- `server`: executable serving entry point.

Implement `runtimev1.RuntimeDriverServer`, embedding `UnimplementedRuntimeDriverServer` by value, and call `server.Serve` from your executable's main function. Hosts use `plugin.Handshake()` and `plugin.ClientPlugins()` with gRPC as the only allowed transport. The host owns executable resolution, startup timeout, cancellation, and `Client.Kill()` cleanup.

The cookie checks identity; it does not authenticate or sandbox the executable. Business diagnostics belong on stderr. Exec stdout/stderr travel only through gRPC frames.

See [the protocol contract](https://devsy.sh/docs/developing-providers/runtime-protocol) for lifecycle, streaming, error, and compatibility semantics.

## Development

```sh
mise install
mise exec -- buf lint
mise exec -- buf format --diff --exit-code
mise exec -- go generate ./...
mise exec -- go test -race ./...
mise exec -- go vet ./...
mise exec -- golangci-lint run
```

Generation pins both Go plugins and protoc. Generated files are checked in, so SDK consumers do not need protoc. CI validates and formats the schema using the declared `proto/` import root, regenerates bindings, and rejects drift. Git hooks are configured in `prek.toml`; run `mise exec -- prek run --all-files` before committing.

Tests build and launch a real plugin executable, including a path with spaces. They cover negotiation, Info, binary Exec channels, terminal exit, cancellation, and process reaping. The executable is a transport fixture, not the B2 fake runtime or the B3 conformance suite. Full lifecycle conformance, process-tree stress, and host startup benchmarks are the next workstream gates.
