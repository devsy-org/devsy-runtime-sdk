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

## Plugin logging

Use `log.NewPluginLogger` to write structured provider diagnostics to stderr:

```go
import pluginlog "github.com/devsy-org/devsy-runtime-sdk/log"

logger := pluginlog.NewPluginLogger("example")
logger.Info("workspace started", "workspace_id", workspaceID)
```

The logger uses newline-delimited JSON with `ts`, `level`, and `msg` fields by default. It adds `component=provider` and the supplied provider name. `DEVSY_LOG_LEVEL` accepts `error`, `warn`, `info`, `debug`, or `trace`, and defaults to `info`. `DEVSY_LOG_FORMAT` accepts `json`, `text`, `plain`, `logfmt`, or `auto`. Empty and `auto` select JSON; `text`, `plain`, and `logfmt` select text. Values are case-insensitive and whitespace is not trimmed. Each invalid field independently falls back to `info` for the level or JSON for the format. The SDK emits one warning per invalid field when constructing a logger, without including the invalid value, even if the configured level would normally filter warnings. Configuration is read when each logger is constructed.

At the root, caller attributes and group names `ts`, `time`, `level`, `msg`, `message`, `component`, and `provider` receive a `user_` prefix. This also applies to attributes in inline groups. Nested named groups retain their original keys. Reserve the `user_` prefix for collision escaping to avoid duplicate keys.

The logger masks values selected from credential-like environment variable names and recognizable URL userinfo and Authorization header credentials. This applies to diagnostic messages, keys, groups, and nested values. It cannot discover every secret. Avoid logging sensitive input, and retain host-side redaction as a separate boundary. Logging a fatal severity does not terminate the process.

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
