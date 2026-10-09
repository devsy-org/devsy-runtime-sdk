package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	detachedFixtureRole   = "DEVSY_DETACHED_FIXTURE_ROLE"
	detachedFixtureSocket = "DEVSY_DETACHED_FIXTURE_SOCKET"
)

func TestRunnerDoesNotWaitForDetachedBackend(t *testing.T) {
	options := fixtureOptions(t)
	options.Args = []string{"-test.run=^TestDetachedBackendFixture$"}
	socket := filepath.Join(shortDirectory(t), "backend.sock")
	options.Env = append(
		os.Environ(),
		detachedFixtureRole+"=runtime",
		detachedFixtureSocket+"="+socket,
	)
	runtime, err := Runner(options)(nil, &exec.Cmd{}, shortDirectory(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stdout().Close(); _ = runtime.Stderr().Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Kill(context.Background()) })
	var pid int
	if err := json.NewDecoder(runtime.Stdout()).Decode(&pid); err != nil {
		t.Fatal(err)
	}
	// The backend has its own session and lifetime. Release it before waiting
	// for supervisor cleanup even when the regression makes Wait time out.
	t.Cleanup(func() { _ = unix.Kill(pid, unix.SIGKILL) })
	if err := runtime.Wait(ctx); err != nil {
		t.Fatalf("supervisor waited for detached backend: %v", err)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		t.Fatalf("detached backend unavailable after session cleanup: %v", err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(connection)
	if err != nil || string(data) != "alive" {
		t.Fatalf("detached backend did not respond: %q (%v)", data, err)
	}
}

func TestDetachedBackendFixture(t *testing.T) {
	switch os.Getenv(detachedFixtureRole) {
	case "backend":
		serveDetachedBackend(t)
	case "runtime":
		runDetachedBackend(t)
	}
}

func serveDetachedBackend(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("unix", os.Getenv(detachedFixtureSocket))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "alive"); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	_ = listener.Close()
	time.Sleep(time.Minute)
	os.Exit(0)
}

func runDetachedBackend(t *testing.T) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Relaunches this test executable in a separate backend session.
	child := exec.Command(binary, "-test.run=^TestDetachedBackendFixture$")
	child.Env = append(os.Environ(), detachedFixtureRole+"=backend")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := json.NewDecoder(stdout).Decode(&pid); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, pid); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
