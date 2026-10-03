package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type inspection struct {
	Args           []string
	Env, Directory string
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--helper-role=supervisor":
			Main(os.Args[2:])
		case "--helper-role=inspect":
			directory, err := os.Getwd()
			if err != nil {
				panic(err)
			}
			err = json.NewEncoder(os.Stdout).
				Encode(inspection{Args: os.Args[2:], Env: os.Getenv("DEVSY_OWNERSHIP_TEST"), Directory: directory})
			if err != nil {
				panic(err)
			}
			if _, err := os.Stderr.Write(
				bytes.Repeat([]byte("diagnostic tail\n"), 64),
			); err != nil {
				panic(err)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func shortDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dso-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		SupervisorBinary: binary, SupervisorArgs: []string{"--helper-role=supervisor"},
		RuntimeBinary: binary, Args: []string{"--helper-role=inspect", "space λ argument"},
		Env: []string{"DEVSY_OWNERSHIP_TEST=inherited override"}, Directory: t.TempDir(),
	}
}

func TestRunnerPreservesConfigurationAndDiagnosticTail(t *testing.T) {
	t.Setenv("DEVSY_OWNERSHIP_TEST", "host-value")
	options := fixtureOptions(t)
	factory := Runner(options)
	options.Args[1] = "changed after factory construction"
	options.Env[0] = "DEVSY_OWNERSHIP_TEST=changed"
	dir := shortDirectory(t)
	runtime, err := factory(nil, &exec.Cmd{}, dir)
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
	// Reaping before draining must preserve all buffered output, including stderr's last bytes.
	if err := runtime.Wait(ctx); err != nil {
		diagnostics, _ := io.ReadAll(runtime.Stderr())
		t.Fatalf("supervisor exit: %v; %s", err, diagnostics)
	}
	var got inspection
	if err := json.NewDecoder(runtime.Stdout()).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, runtime.Stdout()); err != nil {
		t.Fatal(err)
	}
	assertInspection(t, got, options.Directory)
	assertDiagnosticTail(t, runtime.Stderr())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("socket directory survived: %v", err)
	}
}

func TestInvalidOptionsFailBeforeLaunch(t *testing.T) {
	for _, change := range []func(*Options){
		func(o *Options) { o.RuntimeBinary = "runtime-from-PATH" },
		func(o *Options) { o.SupervisorBinary = "supervisor-from-PATH" },
		func(o *Options) { o.Directory = "relative-directory" },
		func(o *Options) { o.RuntimeBinary = filepath.Join(t.TempDir(), "missing.exe") },
	} {
		options := fixtureOptions(t)
		change(&options)
		dir := shortDirectory(t)
		if _, err := Runner(options)(nil, &exec.Cmd{}, dir); err == nil {
			t.Fatal("invalid options accepted")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("failed launch leaked socket directory: %v", err)
		}
	}
}

func TestPreCanceledStartClosesResources(t *testing.T) {
	options := fixtureOptions(t)
	dir := shortDirectory(t)
	runtime, err := Runner(options)(nil, &exec.Cmd{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Start(ctx); err != context.Canceled {
		t.Fatalf("canceled start: %v", err)
	}
	if runtime.ID() != "" {
		t.Fatal("canceled start launched a process")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("canceled launch leaked directory: %v", err)
	}
}

func assertInspection(t *testing.T, got inspection, directory string) {
	t.Helper()
	if len(got.Args) != 1 || got.Args[0] != "space λ argument" || got.Env != "inherited override" {
		t.Fatalf("configuration changed: %+v", got)
	}
	expected, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Stat(got.Directory)
	if err != nil || !os.SameFile(expected, actual) {
		t.Fatalf("wrong working directory: %s (%v)", got.Directory, err)
	}
}

func assertDiagnosticTail(t *testing.T, input io.Reader) {
	t.Helper()
	stderr, err := io.ReadAll(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stderr, bytes.Repeat([]byte("diagnostic tail\n"), 64)) {
		t.Fatal("diagnostic tail lost")
	}
}
