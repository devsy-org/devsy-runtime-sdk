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
