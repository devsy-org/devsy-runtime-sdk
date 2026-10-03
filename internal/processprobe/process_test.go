// Package processprobe_test records the limits of plain go-plugin process ownership.
package processprobe_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devsy-process-probe-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "process λ fixture.exe")
	// #nosec G204 -- Fixed fixture package; output path is owned by the test harness.
	cmd := exec.Command("go", "build", "-race", "-o", executable, "../processfixture")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func launch(t *testing.T, o *observer, ignore bool) (*hplugin.Client, *exec.Cmd) {
	t.Helper()
	// #nosec G204 -- Fixed fixture built by TestMain; no user-controlled executable.
	cmd := exec.Command(executable, "--mode", "plugin", "--observer", o.listener.Addr().String(),
		fmt.Sprintf("--ignore-interrupt=%t", ignore))
	cmd.Env = fixtureEnvironment(o.directory)
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		SkipHostEnv:     true,
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
	t.Cleanup(client.Kill)
	return client, cmd
}

func connect(t *testing.T, client *hplugin.Client) runtimev1.RuntimeDriverClient {
	t.Helper()
	rpc, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rpc.Dispense(sdkplugin.Name)
	if err != nil {
		t.Fatal(err)
	}
	driver, ok := raw.(runtimev1.RuntimeDriverClient)
	if !ok {
		t.Fatalf("unexpected client %T", raw)
	}
	return driver
}

func operation(
	ctx context.Context,
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
	unary bool,
) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	if unary {
		go func() { _, err := driver.Info(ctx, &runtimev1.InfoRequest{}); result <- err }()
		return result
	}
	stream, err := driver.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{WorkspaceId: "process-probe", Argv: []string{"block"}},
	}}); err != nil {
		t.Fatal(err)
	}
	go func() { _, err := stream.Recv(); result <- err }()
	return result
}

func result(t *testing.T, errors <-chan error) error {
	t.Helper()
	select {
	case err := <-errors:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("operation did not terminate")
		return nil
	}
}

func TestCancellationReapsRuntimeChild(t *testing.T) {
	for _, unary := range []bool{true, false} {
		for _, ignore := range []bool{false, true} {
			t.Run(fmt.Sprintf("unary=%t/ignore-interrupt=%t", unary, ignore), func(t *testing.T) {
				o := observe(t)
				client, _ := launch(t, o, ignore)
				driver := connect(t, client)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				pending := operation(ctx, t, driver, unary)
				child := o.readyChild(t)
				child.alive(t)
				cancel()
				if err := result(t, pending); status.Code(err) != codes.Canceled {
					t.Fatalf("cancel: %v", err)
				}
				// A canceled RPC can return before the plugin finishes cleanup: wait for the actual Wait acknowledgement.
				awaitIgnored(t, o, ignore)
				reaped := o.await(t, "plugin", "reaped")
				if reaped.PID != child.pid {
					t.Fatal("wrong process was reaped")
				}
				child.exited(t)
				client.Kill()
				if !client.Exited() {
					t.Fatal("plugin was not reaped")
				}
				o.lookup(t, "plugin").exited(t)
			})
		}
	}
}

func TestPluginCrashLeavesRuntimeChild(t *testing.T) {
	o := observe(t)
	client, cmd := launch(t, o, true)
	driver := connect(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pending := operation(ctx, t, driver, false)
	child := o.readyChild(t)
	child.alive(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := result(t, pending); status.Code(err) != codes.Unavailable {
		t.Fatalf("crash: %v", err)
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("plugin was not reaped")
	}
	o.lookup(t, "plugin").exited(t)
	// This is a measured ownership gap, not a production guarantee. The observer prevents a test orphan.
	child.alive(t)
	t.Log("plain go-plugin cannot clean a runtime child after abrupt plugin death")
	child.kill(t)
}

func TestHostDeathLeavesPlugin(t *testing.T) {
	o := observe(t)
	// #nosec G204 -- Fixed fixture built by TestMain, with a test-owned observer.
	cmd := exec.Command(
		executable,
		"--mode",
		"host",
		"--observer",
		o.listener.Addr().String(),
		"--ignore-interrupt=true",
	)
	cmd.Env = fixtureEnvironment(o.directory)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	child := o.readyChild(t)
	plugin := o.lookup(t, "plugin")
	child.alive(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed host reported success")
	}
	o.lookup(t, "host").exited(t)
	// Transport loss cancels the RPC, allowing a cooperative plugin to reap its child.
	o.await(t, "plugin", "reaped")
	child.exited(t)
	plugin.alive(t)
	t.Log(
		"transport cancellation cleans the child, but abrupt host death leaves the plugin serving",
	)
	plugin.kill(t)
}

func awaitIgnored(t *testing.T, o *observer, ignore bool) {
	t.Helper()
	if ignore && runtime.GOOS != "windows" {
		o.await(t, "child", "ignored")
	}
}

func fixtureEnvironment(directory string) []string {
	// Keep inherited compatibility, while placing crash-left socket files in a short, test-owned directory.
	return append(os.Environ(), "TMPDIR="+directory, "TEMP="+directory, "TMP="+directory)
}
