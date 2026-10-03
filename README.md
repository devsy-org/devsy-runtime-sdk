# Devsy Runtime SDK

Go bindings and gRPC serving tools for external Devsy runtime drivers. The SDK defines Runtime Protocol v1 and lets a host launch a runtime executable using [go-plugin](https://github.com/hashicorp/go-plugin).

Runtime drivers implement resource lifecycle operations and command execution. Devsy handles image preparation, Dev Container orchestration, agent delivery, and workspace state. The SDK is an independent Go module; consumers do not need to import Devsy itself.

## Installation

Requires Go 1.26 or newer:

```sh
go get github.com/devsy-org/devsy-runtime-sdk
```

Generated protobuf bindings are included. Consumers do not need protoc or the development tools listed below.

## Packages

- `runtimev1`: protobuf/gRPC bindings, API versions, and `ValidateInfo` for compatibility checks.
- `plugin`: shared handshake, plugin name, and gRPC registration/client bridge.
- `server`: executable serving entry point.
- `conformance`: reusable protocol behavior suite and structured-error assertions.
- `conformance/fake`: persistent fake driver for host integration tests.

## Implementing a runtime

Implement `runtimev1.RuntimeDriverServer`, embedding `UnimplementedRuntimeDriverServer` by value, and call `server.Serve` from your executable's main function. This minimal example implements discovery:

```go
package main

import (
    "context"

    "github.com/devsy-org/devsy-runtime-sdk/runtimev1"
    "github.com/devsy-org/devsy-runtime-sdk/server"
)

type driver struct {
    runtimev1.UnimplementedRuntimeDriverServer
}

func (*driver) Info(context.Context, *runtimev1.InfoRequest) (*runtimev1.InfoResponse, error) {
    return &runtimev1.InfoResponse{
        ApiMajor:      runtimev1.APIMajor,
        ApiMinor:      runtimev1.APIMinor,
        DriverName:    "example-runtime",
        DriverVersion: "0.1.0",
        RuntimeName:   "example",
        Capabilities: &runtimev1.Capabilities{
            RecreateMode: runtimev1.RecreateMode_RECREATE_MODE_STOP,
        },
    }, nil
}

func main() {
    server.Serve(&driver{})
}
```

The remaining RPCs return `Unimplemented` until you override them. A usable driver must implement lifecycle operations and Exec with behavior consistent with its advertised capabilities.

Hosts configure a go-plugin client with `plugin.Handshake()`, `plugin.ProtocolVersion`, and `plugin.ClientPlugins()`, allowing only `go-plugin.ProtocolGRPC`. The host owns trusted executable resolution, startup timeout, cancellation, and `Client.Kill()` cleanup. Call `runtimev1.ValidateInfo` before invoking runtime operations: API majors must match, while newer minor versions within the same major are compatible.

The handshake cookie checks identity; it does not authenticate or sandbox the executable. Business diagnostics belong on stderr. Exec stdout and stderr travel through gRPC frames, preserve binary data, and end with a terminal exit frame. Cancellation must stop the command and release its resources.

See the [Runtime Protocol reference](https://devsy.sh/docs/developing-providers/runtime-protocol) for the complete lifecycle, streaming, error, and compatibility contract.

## Development

```sh
mise install
mise exec -- prek install
mise exec -- prek run --all-files
mise exec -- go test -race ./...
mise exec -- go vet ./...
```

`prek.toml` runs Go linting and formatting plus protobuf schema checks. Linting follows Devsy's rules, including complexity, error handling, security, and API style checks. To apply Go formatting, run `mise exec -- golangci-lint fmt`.

After changing the schema, regenerate the checked-in bindings:

```sh
mise exec -- go generate ./...
```

Generation pins protoc and both Go plugins. CI runs independent Pre-commit (prek) and Lint checks, rejects generated-code drift, and runs race-enabled tests on Linux, macOS, and Windows. Tests launch a real plugin executable and cover negotiation, discovery, binary Exec channels, terminal exit, cancellation, and process cleanup. These transport tests do not certify a driver's complete lifecycle implementation.

## Fake runtime for integration tests

Build a standalone fixture that uses the real go-plugin/gRPC stack:

```sh
go build -o devsy-fake-runtime ./cmd/devsy-fake-runtime
```

Launch it through the SDK plugin client with `--state-dir <private-directory>`
and optional `--mode <scenario>` arguments. Each directory must have only one
active plugin owner. Workspace state survives plugin termination and relaunch;
use a fresh directory for independent tests. This fixture simulates resources
and echoes stdin; it does not run containers or interpret commands.

| Mode | Behavior |
| --- | --- |
| `conformance` | Select a suite scenario through Exec argv; see below |
| `normal`, `exec-echo` | Full lifecycle, stdin echoed to stdout, exit 0 |
| `crash-before-handshake` | Exit before serving the plugin |
| `crash-after-handshake` | Exit on the first Info call |
| `delay-handshake` | Delay serving; configure with `--handshake-delay` (default 1m) |
| `incompatible-version` | Info reports an incompatible API major |
| `malformed-info` | Info omits the required driver name |
| `fail-preflight` | Both preflight calls return structured Unavailable errors |
| `fail-run` | RunImage fails without creating a workspace |
| `not-found` | Find reports absence even for persisted workspaces |
| `exec-nonzero` | Echo stdin, emit stderr diagnostics, exit 7 |
| `exec-crash` | Exit the plugin after receiving ExecStart |
| `exec-slow` | Block Exec until its context is canceled |
| `logs` | Normal lifecycle and deterministic binary merged logs |

By default, all modes advertise bind, volume, tmpfs, provisioning preflight, and
Logs support. Select mount profiles with `--mount-types all|none|bind|volume|tmpfs`,
or a comma-separated subset. Unsupported mounts fail before workspace creation.
Exec and Logs require a running workspace. Start and Stop are idempotent for
existing resources; Delete is idempotent even for absent resources. Duplicate
RunImage returns AlreadyExists. Lifecycle failures include RuntimeError details.
Output frames are limited to 32 KiB and preserve binary bytes.

The SDK tests exercise lifecycle state across real plugin restarts, fault modes,
binary streaming, cancellation, and process cleanup on the CI operating-system
matrix. These fixture checks are the foundation for the broader driver
conformance suite, not certification of an external runtime implementation.

## Runtime conformance suite

Runtime authors can call `conformance.Run(t, options)` from a Go test. Supply
three adapters, all using the runtime's real client transport:

```go
conformance.Run(t, conformance.Options{
    Connect:   launchRuntimeClient, // func(*testing.T) runtimev1.RuntimeDriverClient
    Workspace: workspaceRequest,   // func(*testing.T) *runtimev1.RunImageRequest
    Command:   scenarioCommand,     // func(conformance.Scenario, string) *runtimev1.ExecStart
    LogData:   []byte("expected merged logs\n"),
})
```

`Connect` must create a fresh client per subtest and register bounded process and
connection cleanup with `t.Cleanup`. `Workspace` must provide an image and a
unique workspace ID that is initially absent. Configure the image's entrypoint
so its finite log output matches `LogData` if the runtime advertises Logs.
`Command` receives the scenario and workspace ID; return exact argv with
`tty=false`. The suite does not choose a shell or construct commands for you.
The default RPC timeout is 30 seconds; set `Timeout` for slower real runtimes.

The suite checks discovery and architecture, lifecycle idempotency, absent
resources, malformed Exec frames, binary preservation, separate output channels,
12 MiB of simultaneous stdin/stdout/stderr, consumer backpressure, early exit,
nonzero and signal exits, cancellation, deadlines, and finite merged Logs.
Logs and provisioning preflight checks follow the advertised capabilities.
Streaming comparisons use bounded buffers and require a single terminal exit
frame after all output. Data frames must be nonempty and follow the SDK's
recommended 32 KiB bound. Send goroutines are canceled and joined on failure.

Implement these command scenarios in the adapters:

| Scenario | Required behavior |
| --- | --- |
| `Echo` | Copy stdin bytes to stdout until CloseStdin; exit 0 |
| `Arguments` | Print each value from `conformance.ArgumentValues()` verbatim, followed by NUL; exit 0 |
| `Shell` | Execute a shell command that prints `shell stdout\n`; exit 0 |
| `Stderr` | Copy stdin bytes to stderr only; exit 0 |
| `Duplex` | Copy every stdin byte to both stdout and stderr; exit 0 |
| `Nonzero` | Print `diagnostic` to stderr; exit with a nonzero code and no signal |
| `Signal` | Report a nonempty exit signal; any exit code is accepted |
| `EarlyExit` | Exit 0 without reading stdin or producing output |
| `Block` | Print `ready\n` to stdout, then block until context cancellation; do not exit normally |

For runtime-specific failure injection, call
`conformance.RequireRuntimeError(t, err, expectedCategory)`. It verifies the
canonical gRPC status, exactly one typed RuntimeError detail, the expected
category, and a nonempty user-facing message. The returned detail supports
additional retryability and diagnostic assertions.

The fake executable's `conformance` mode uses scenario names as argv[0], with
`conformance.ArgumentValues()` appended for `arguments`. Its shell scenario
accepts exactly `/bin/sh`, `-c`, `printf 'shell stdout\n'`; the fake simulates
that output rather than executing a host shell. `error <CATEGORY>` injects a
structured error, and `crash` emits readiness then exits after one input frame.
CI runs the full reusable suite against this executable on Linux, macOS, and
Windows, along with fault, mount-profile, crash/restart, and process-reap tests.

Host launcher trust, startup cancellation, descendant process ownership,
benchmarking, and Devsy-specific agent delivery are separate host adapter checks;
this suite does not certify those policies or runtime-specific image semantics.
