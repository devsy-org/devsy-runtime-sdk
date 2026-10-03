// Process fixture only: exercises ownership failures through the real SDK transport.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const (
	childRole  = "child"
	pluginRole = "plugin"
)

type event struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
}

type fixture struct {
	runtimev1.UnimplementedRuntimeDriverServer
	observer string
	ignore   bool
	events   *reporter
}

func main() {
	mode := flag.String("mode", pluginRole, "fixture role")
	observer := flag.String("observer", "", "loopback observer address")
	ignore := flag.Bool("ignore-interrupt", false, "child ignores normal termination")
	flag.Parse()
	if err := run(*mode, *observer, *ignore); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mode, observer string, ignore bool) error {
	conn, err := net.DialTimeout("tcp", observer, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	events := &reporter{encoder: json.NewEncoder(conn), role: mode}
	if err := events.Encode(event{Role: mode, Kind: "ready", PID: os.Getpid()}); err != nil {
		return err
	}
	disconnected := make(chan struct{})
	go events.respond(conn, disconnected)
	switch mode {
	case childRole:
		return child(disconnected, events, ignore)
	case "host":
		return host(observer, ignore)
	case pluginRole:
		server.Serve(&fixture{observer: observer, ignore: ignore, events: events})
		return nil
	default:
		return errors.New("unknown fixture role")
	}
}

func child(disconnected <-chan struct{}, events *reporter, ignore bool) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	// Publish readiness only after the signal handler and observer connection exist.
	if err := json.NewEncoder(os.Stdout).
		Encode(event{Role: childRole, Kind: "armed", PID: os.Getpid()}); err != nil {
		return err
	}
	for {
		select {
		case <-disconnected:
			return nil
		case <-signals:
			if !ignore {
				return nil
			}
			if err := events.Encode(
				event{Role: childRole, Kind: "ignored", PID: os.Getpid()},
			); err != nil {
				return err
			}
		}
	}
}

func (f *fixture) Info(
	ctx context.Context,
	_ *runtimev1.InfoRequest,
) (*runtimev1.InfoResponse, error) {
	if err := f.block(ctx); err != nil {
		return nil, err
	}
	return nil, status.FromContextError(ctx.Err()).Err()
}

func (f *fixture) Exec(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetStart() == nil {
		return errors.New("missing Exec start")
	}
	return f.block(stream.Context())
}

func (f *fixture) block(ctx context.Context) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// #nosec G204 -- Self-executable fixture, with arguments controlled by the test harness.
	cmd := exec.CommandContext(ctx, executable, "--mode", childRole, "--observer", f.observer,
		fmt.Sprintf("--ignore-interrupt=%t", f.ignore))
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	// Windows cannot deliver os.Interrupt to a child; WaitDelay also bounds this fallback.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		return err
	}
	var ready event
	if err := json.NewDecoder(out).Decode(&ready); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := f.events.Encode(
		event{Role: pluginRole, Kind: "child-ready", PID: ready.PID},
	); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	// Wait is required even when cancellation or forced termination has occurred.
	waitErr := cmd.Wait()
	if err := f.events.Encode(event{Role: pluginRole, Kind: "reaped", PID: ready.PID}); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	return waitErr
}

func host(observer string, ignore bool) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// #nosec G204 -- Self-executable fixture, with arguments controlled by the test harness.
	cmd := exec.Command(executable, "--mode", pluginRole, "--observer", observer,
		fmt.Sprintf("--ignore-interrupt=%t", ignore))
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		Cmd:              cmd,
		StartTimeout:     10 * time.Second,
		Logger:           hclog.NewNullLogger(),
		Stderr:           os.Stderr,
		SyncStderr:       os.Stderr,
	})
	defer client.Kill()
	rpc, err := client.Client()
	if err != nil {
		return err
	}
	raw, err := rpc.Dispense(sdkplugin.Name)
	if err != nil {
		return err
	}
	driver, ok := raw.(runtimev1.RuntimeDriverClient)
	if !ok {
		return errors.New("unexpected runtime client")
	}
	stream, err := driver.Exec(context.Background())
	if err != nil {
		return err
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{WorkspaceId: "process-probe", Argv: []string{"block"}},
	}}); err != nil {
		return err
	}
	_, err = stream.Recv()
	return err
}
