package processprobe_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func owned(t *testing.T, o *observer, delayed bool) *hplugin.Client {
	t.Helper()
	args := []string{
		"--mode",
		"plugin",
		"--observer",
		o.listener.Addr().String(),
		"--ignore-interrupt=true",
		"--grandchild=true",
		"--uncancelable=true",
	}
	timeout := 10 * time.Second
	if delayed {
		args = append(args, "--delayed=true")
		timeout = time.Second
	}
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		RunnerFunc: supervisor.Runner(supervisor.Options{
			SupervisorBinary: executable,
			SupervisorArgs:   []string{"--supervise", o.listener.Addr().String()},
			RuntimeBinary:    executable,
			Args:             args,
			Env: append(
				fixtureEnvironment(o.directory),
				sdkplugin.Handshake().MagicCookieKey+"=stale-cookie",
				"PLUGIN_PROTOCOL_VERSIONS=99",
				"PLUGIN_UNIX_SOCKET_DIR="+filepath.Join(o.directory, "stale"),
			),
		}),
		StartTimeout:     timeout,
		Logger:           hclog.NewNullLogger(),
		Stderr:           os.Stderr,
		SyncStderr:       os.Stderr,
		UnixSocketConfig: &hplugin.UnixSocketConfig{TempDir: o.directory},
	})
	t.Cleanup(client.Kill)
	return client
}

func ownedOperation(t *testing.T, o *observer, client *hplugin.Client) <-chan error {
	t.Helper()
	driver := connect(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pending := operation(ctx, t, driver, false)
	o.readyChild(t).alive(t)
	o.await(t, "grandchild", "ready")
	o.lookup(t, "grandchild").alive(t)
	return pending
}

func assertOwnedCleanup(t *testing.T, o *observer) {
	t.Helper()
	for _, role := range []string{"supervisor", "plugin", "child", "grandchild"} {
		o.lookup(t, role).exited(t)
	}
	entries, err := filepath.Glob(filepath.Join(o.directory, "plugin-dir*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("supervisor socket directory leaked: %v (%v)", entries, err)
	}
}

func TestOwnedPluginCrashTerminatesDescendants(t *testing.T) {
	o := observe(t)
	client := owned(t, o, false)
	pending := ownedOperation(t, o, client)
	if err := o.lookup(t, "plugin").handle.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := result(t, pending); status.Code(err) != codes.Unavailable {
		t.Fatalf("crash: %v", err)
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("supervisor was not reaped")
	}
	assertOwnedCleanup(t, o)
}

func TestOwnedCloseTerminatesUncooperativeDescendants(t *testing.T) {
	o := observe(t)
	client := owned(t, o, false)
	pending := ownedOperation(t, o, client)
	client.Kill()
	if err := result(t, pending); err == nil {
		t.Fatal("terminated Exec reported success")
	}
	if !client.Exited() {
		t.Fatal("supervisor was not reaped")
	}
	assertOwnedCleanup(t, o)
}

func TestOwnedHostDeathTerminatesPluginAndDescendants(t *testing.T) {
	o := observe(t)
	// #nosec G204 -- Fixed fixture built by TestMain; arguments are test-owned.
	cmd := exec.Command(executable, "--mode", "host", "--observer", o.listener.Addr().String(),
		"--owned=true", "--grandchild=true", "--uncancelable=true", "--ignore-interrupt=true")
	cmd.Env = fixtureEnvironment(o.directory)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	o.readyChild(t).alive(t)
	o.await(t, "grandchild", "ready")
	o.lookup(t, "grandchild").alive(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed host reported success")
	}
	o.lookup(t, "host").exited(t)
	assertOwnedCleanup(t, o)
}

func TestOwnedHandshakeTimeoutReapsProcesses(t *testing.T) {
	o := observe(t)
	client := owned(t, o, true)
	if _, err := client.Client(); err == nil {
		t.Fatal("delayed handshake succeeded")
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("timed-out supervisor was not reaped")
	}
	o.await(t, "plugin", "ready")
	o.lookup(t, "plugin").exited(t)
	o.lookup(t, "supervisor").exited(t)
}
