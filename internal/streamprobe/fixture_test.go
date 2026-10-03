package streamprobe_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
)

var executable string

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--stream-supervisor" {
		supervisor.Main(os.Args[2:])
	}
	dir, err := os.MkdirTemp("", "runtime stream λ ")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "stream runtime.exe")
	// #nosec G204 -- Fixed fixture package, output in a test-owned directory.
	cmd := exec.Command("go", "build", "-race", "-o", executable, "../streamfixture")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func launch(t *testing.T) (*hplugin.Client, runtimev1.RuntimeDriverClient) {
	t.Helper()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "ds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Errorf("socket cleanup: %v", err)
		}
	})
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		RunnerFunc: supervisor.Runner(supervisor.Options{
			SupervisorBinary: helper,
			SupervisorArgs:   []string{"--stream-supervisor"},
			RuntimeBinary:    executable,
		}),
		StartTimeout: 30 * time.Second,
		Logger:       hclog.NewNullLogger(),
		Stderr:       os.Stderr, SyncStderr: os.Stderr,
		UnixSocketConfig: &hplugin.UnixSocketConfig{TempDir: socketDir},
	})
	t.Cleanup(client.Kill)
	transport, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := transport.Dispense(sdkplugin.Name)
	if err != nil {
		t.Fatal(err)
	}
	driver, ok := raw.(runtimev1.RuntimeDriverClient)
	if !ok {
		t.Fatalf("unexpected driver %T", raw)
	}
	return client, driver
}

func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func start(
	ctx context.Context,
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
	command string,
) runtimev1.RuntimeDriver_ExecClient {
	t.Helper()
	s, err := driver.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{WorkspaceId: "probe", Argv: []string{command}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
