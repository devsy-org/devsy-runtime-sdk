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
- `supervisor`: an opt-in go-plugin runner that owns a leased plugin process tree.
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

### Workspace identity in API 1.1

`RunImageRequest.remote_user` carries the developer identity used for workspace
ownership. It is distinct from `user`, which selects the container process user.
When `remote_user` is empty, use `user` if set, otherwise `root`.
`dockerless` means the host will provision the developer environment after the
image starts; the final developer identity may not yet exist in that image.
Runtimes that resolve mount ownership from image contents must validate this
case before making changes to workspace resources.

Hosts must forward both values and runtimes must handle them before enabling
an implementation that depends on workspace ownership. These fields describe
provisioning intent; they do not authorize resource replacement. Duplicate
creation and explicit Delete remain separate operations.

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

## Measuring plugin startup

The host adapter's startup spike uses the real plugin transport and includes
process shutdown and reaping. Build all three executables without race instrumentation
(the race runtime's exit delay would distort process lifetime measurements):

```sh
mkdir -p bin/probe-state
go build -o bin/devsy-fake-runtime ./cmd/devsy-fake-runtime
go build -o bin/devsy-runtime-spawn-probe ./cmd/devsy-runtime-spawn-probe
go build -o bin/devsy-runtime-supervisor ./cmd/devsy-runtime-supervisor
"$PWD/bin/devsy-runtime-spawn-probe" \
  --binary "$PWD/bin/devsy-fake-runtime" --samples 100 \
  -- --state-dir "$PWD/bin/probe-state" > bin/spawn-report.json
"$PWD/bin/devsy-runtime-spawn-probe" \
  --binary "$PWD/bin/devsy-fake-runtime" \
  --supervisor-binary "$PWD/bin/devsy-runtime-supervisor" --samples 100 \
  -- --state-dir "$PWD/bin/probe-state" > bin/supervised-spawn-report.json
```

The probe requires an explicitly trusted absolute executable path and an unused
workspace ID for Find. It inherits the environment for compatibility; it does
not perform provider download verification or sandbox the executable. Only Info
and Find are called: the probe does not create, start, stop, or delete resources.
Forward runtime arguments after `--`. Plugin diagnostics stay on stderr; stdout
contains only the JSON report. `--timeout` bounds startup and RPCs per repetition;
Kill/reap is measured separately using go-plugin's cleanup behavior.

Each operation has one first-launch sample and 100 subsequent warm samples.
Info and Find each launch a fresh process, perform handshake/dial and dispense,
make one RPC, and kill/reap that process. The startup stage combines process
launch, handshake, and gRPC dial because go-plugin exposes them as one client
initialization call. Find measures ordinary absence independently of Info.
Reports retain raw stage timings in nanoseconds, total p50/p95/p99 using
nearest-rank percentiles, process IDs and reaping confirmation, OS/architecture,
Go version, CPU count, source revision, and the measured binary's SHA-256.
The additive `launch_mode` field identifies `direct` or `supervised` launches;
`supervisor_sha256` identifies the helper in supervised reports. In direct mode,
`pid` identifies the plugin; in supervised mode it identifies the owning helper.
Startup and total durations in supervised mode include launching both processes,
and reaping includes waiting for supervisor cleanup. Arguments, full environment
values, and executable paths are omitted.

The binary hash identifies the measured artifact; it is not an integrity check
against a trusted expected checksum. Hashing also warms file caches. First
launches are therefore not cold-cache measurements, and reports explicitly set
`cold_cache_measured=false`. Cold-cache experiments require a separately documented
procedure on hosts where cache disruption is practical. Compare these results
with an ordinary Devsy up trace before selecting per-operation or Runner-owned
plugin lifetime; this probe makes no ownership decision or latency threshold.

CI runs the probe on Linux, macOS, and Windows with executable paths containing
spaces and non-ASCII characters, and publishes `runtime-spawn-*` JSON artifacts.
Each artifact retains the direct `report.json` and `supervised-report.json` from
the same runner, runtime binary, operation, and sample count. Compare Info and
Find separately. These are sequential warm-cache experiments (direct runs first),
not randomized trials, cold-cache results, or latency acceptance tests. Repeat in
both orders on a representative host before drawing a performance conclusion.
These jobs gate release automation alongside the existing quality checks;
successful startup measurements alone do not certify descendant-process cleanup.

## Process ownership experiments

Run the real-process cancellation and crash probes with:

```sh
mise exec -- go test -race -v ./internal/processprobe
```

The fixture launches a host, a plugin, and a blocking runtime child from an
executable path containing spaces and Unicode. Readiness acknowledgements
precede cancellation, and independent observer connections answer liveness
checks. The plugin acknowledges child reaping only after `exec.Cmd.Wait` returns.
CI runs these probes on Linux, macOS, and Windows with the other race tests;
`runtime-tests-*` artifacts retain their JSON test output.

| Scenario | Behavior asserted by the spike |
| --- | --- |
| Cancel unary RPC or Exec | A cooperative plugin cancels and reaps its child |
| Child ignores interruption | A bounded forced-kill fallback reaps the child; Unix also acknowledges the ignored signal |
| Abrupt plugin death | The runtime child survives until the independent test observer kills it |
| Abrupt host death | Transport loss cancels the RPC and reaps the child, but the plugin survives until the observer kills it |


The last two baseline tests deliberately retain the plain transport's ownership
gaps. The owned-runner regressions add a grandchild and disable the fixture's
response to RPC cancellation. They verify that plugin death, host death, and
explicit client cleanup terminate all three runtime processes without the test
observer killing survivors. A handshake-timeout regression also verifies cleanup
before the runtime becomes ready. The observer remains a fallback only when a
test fails.

## Owning a runtime process tree

Build the dedicated supervisor helper alongside your runtime binary:

```sh
go build -o devsy-runtime-supervisor ./cmd/devsy-runtime-supervisor
```

Configure an SDK client with an explicitly verified absolute path for each
executable:

```go
client := hplugin.NewClient(&hplugin.ClientConfig{
    HandshakeConfig: plugin.Handshake(),
    VersionedPlugins: map[int]hplugin.PluginSet{
        plugin.ProtocolVersion: plugin.ClientPlugins(),
    },
    AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
    RunnerFunc: supervisor.Runner(supervisor.Options{
        SupervisorBinary: verifiedSupervisorPath,
        RuntimeBinary:    verifiedRuntimePath,
        Args:             runtimeArgs,
    }),
    StartTimeout: 15 * time.Second,
})
defer client.Kill()
```

`hplugin` is `github.com/hashicorp/go-plugin`; `plugin` and `supervisor` are SDK
packages. Use `RunnerFunc` instead of `Cmd`. The host verifies both executables
through its existing binary distribution mechanism before configuring this
runner; go-plugin's `SecureConfig` checks a `Cmd` path and is not applicable to
this runner. An embedding host can also expose `supervisor.Main(args)` as a
dedicated helper command in its own executable, selected by `SupervisorArgs`.
`Main` always exits its process and must only run in that helper process.
The SDK's module releases do not distribute helper executables automatically.

One supervisor belongs to one go-plugin client. `Client.Kill()` closes the host
lease and waits for the supervisor to be reaped. Abrupt host death closes the
same pipe through the OS, so cleanup continues without a live host. Plugin exit
also triggers cleanup. There is no global process pool or implicit cache.
Runtime arguments and environment configuration travel to the supervisor through
an inherited pipe; the supervisor forwards handshake stdout and diagnostics
stderr without logging the configuration. Buffered diagnostics remain available
after process reaping, until the reader drains them.

| Platform | Ownership mechanism |
| --- | --- |
| Linux | Dedicated plugin process group; supervisor adopts and reaps orphaned descendants as a subreaper |
| macOS | Dedicated plugin process group; supervisor reaps the plugin and the OS adopts orphaned descendants |
| Windows | Supervisor joins a non-breakaway Job Object before spawning the plugin; descendants inherit membership, and the last handle closes when the supervisor exits |

Unix observes plugin exit before reaping its group leader, keeping the group ID
reserved while terminating descendants. Linux uses `waitid` with `WNOWAIT` and
macOS uses a process-exit kqueue event. Real permission or ownership-setup errors
remain failures; there is no fallback to unowned launch. The Windows Job Object
uses [kill-on-close semantics](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).

This is ownership for trusted runtime commands, not a sandbox. Unix descendants
must remain in the plugin's process group and retain signalable privileges.
Daemonization, a new session/process group (including a separately created PTY
session), or privilege elevation requires an additional explicit owner. On Unix,
simultaneously killing the host and its supervisor prevents that supervisor from
performing cleanup. Long-lived runtime services and container resources must have
their own resource lifecycle rather than depend on these command processes.

The default environment remains inherited, with optional `Env` overrides;
client-assigned handshake, certificate, and socket metadata retain precedence.
`Options.Env` only supplies overrides; listing a few variables there does not
restrict inheritance. A deliberate reduced environment requires go-plugin
`ClientConfig.SkipHostEnv = true` together with explicit `Options.Env` values.
This affects the runtime environment, not the trusted supervisor's own inherited
environment or OS-required variables such as Windows `SYSTEMROOT`.
`Directory` sets the runtime working directory. The additional supervisor start
must be included in startup measurements before selecting session reuse.
Streaming stress and real-runtime trust/environment compatibility remain
separate gates before runtime cutover. The streaming probes below cover the
owned transport; real-runtime compatibility remains outstanding. The
[Runtime Protocol reference](https://devsy.sh/docs/developing-providers/runtime-protocol)
records the planned Devsy host environment and executable trust policy;
external runtime host integration is not implemented yet.

## Streaming stress under process ownership

Run the supervisor-backed transport probes with:

```sh
mise exec -- go test -race -v ./internal/streamprobe
```

Each duplex run transfers 100 MiB of binary stdin and checks 100 MiB on each
output channel, followed by distinct binary tails and exactly one nonzero exit.
Incremental SHA-256 comparisons use fixed-size buffers rather than retaining the
payload. Separate runs pace the host consumer and plugin reader. Each side has
one stream sender; an independent control stream releases the paused reader.

Backpressure probes pause either the plugin input reader or the host output
consumer. They check that the bulk sender cannot finish, record host and plugin
live Go heap after GC, and enforce a 32 MiB growth budget over the connected
baseline. These are retained-heap checkpoints, not peak RSS measurements or a
memory bound for arbitrary runtime implementations. Cancellation must unblock
and join the sender; a control RPC must still succeed. Additional probes cover
command early exit and an actual plugin process crash while stdin is active.

The fixture uses the real go-plugin/gRPC transport and the SDK supervisor, with
runtime executable paths containing spaces and Unicode. CI runs these probes
under the race detector on Linux, macOS, and Windows; their output is retained
in the existing `runtime-tests-*` artifacts. The fixture's command outcomes are
synthetic and do not certify a real runtime's child-process or environment
behavior. Real-runtime compatibility and supervisor startup measurements remain
separate integration gates.
