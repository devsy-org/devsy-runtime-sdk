package fake_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	allMounts = "all"
	testImage = "image"
)

var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devsy-fake-test-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "fake runtime")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	// #nosec G204 -- Fixed package; the output path is created by the test harness.
	cmd := exec.Command("go", "build", "-race", "-o", executable, "../../cmd/devsy-fake-runtime")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func launch(t *testing.T, dir, mode string) *hplugin.Client {
	t.Helper()
	return launchWithTimeout(t, dir, mode, 5*time.Second)
}

func launchWithTimeout(t *testing.T, dir, mode string, timeout time.Duration) *hplugin.Client {
	t.Helper()
	return launchProcess(
		t,
		launchOptions{directory: dir, mode: mode, timeout: timeout, mounts: allMounts},
	)
}

type launchOptions struct {
	directory string
	mode      string
	timeout   time.Duration
	mounts    string
}

func launchProcess(t *testing.T, options launchOptions) *hplugin.Client {
	t.Helper()
	// #nosec G204 -- Executable is built from the fixed fixture package in TestMain.
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		Cmd: exec.Command(
			executable,
			"--state-dir",
			options.directory,
			"--mode",
			options.mode,
			"--mount-types",
			options.mounts,
		),
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		StartTimeout:     options.timeout,
	})
	t.Cleanup(client.Kill)
	return client
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

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func createWorkspace(t *testing.T, driver runtimev1.RuntimeDriverClient) {
	t.Helper()
	_, err := driver.RunImage(testContext(t), &runtimev1.RunImageRequest{
		WorkspaceId: "workspace/../one", Image: "fake:image", User: "1000",
		Labels: []string{"purpose=test=fixture"},
		WorkspaceMount: &runtimev1.Mount{
			Type:   runtimev1.MountType_MOUNT_TYPE_BIND,
			Source: "/source", Target: "/workspace",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

const workspaceID = "workspace/../one"

func TestLifecyclePersistsAcrossProcesses(t *testing.T) {
	ctx := testContext(t)
	dir := t.TempDir()
	process := launch(t, dir, fake.Normal)
	driver := connect(t, process)
	assertState(t, driver, "")
	createWorkspace(t, driver)
	assertState(t, driver, "running")
	process.Kill()
	if !process.Exited() {
		t.Fatal("plugin not reaped")
	}
	driver = connect(t, launch(t, dir, fake.Normal))
	assertState(t, driver, "running")
	for range 2 {
		if _, err := driver.Stop(
			ctx,
			&runtimev1.StopRequest{WorkspaceId: workspaceID},
		); err != nil {
			t.Fatal(err)
		}
		assertState(t, driver, "stopped")
	}
	for range 2 {
		if _, err := driver.Start(
			ctx,
			&runtimev1.StartRequest{WorkspaceId: workspaceID},
		); err != nil {
			t.Fatal(err)
		}
		assertState(t, driver, "running")
	}
	for range 2 {
		if _, err := driver.Delete(
			ctx,
			&runtimev1.DeleteRequest{WorkspaceId: workspaceID},
		); err != nil {
			t.Fatal(err)
		}
		assertState(t, driver, "")
	}
	_, err := driver.Start(ctx, &runtimev1.StartRequest{WorkspaceId: workspaceID})
	assertRuntimeError(
		t,
		err,
		codes.NotFound,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND,
	)
}

func assertState(t *testing.T, driver runtimev1.RuntimeDriverClient, state string) {
	t.Helper()
	found, err := driver.Find(testContext(t), &runtimev1.FindRequest{WorkspaceId: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if found.GetFound() != (state != "") || found.GetContainer().GetState().GetStatus() != state {
		t.Fatalf("unexpected state: %v", found)
	}
	if state != "" && (found.GetContainer().GetConfig().GetUser() != "1000" ||
		found.GetContainer().GetConfig().GetLabels()["purpose"] != "test=fixture" ||
		len(found.GetContainer().GetMounts()) != 1) {
		t.Fatal("container configuration lost")
	}
}

func assertRuntimeError(
	t *testing.T,
	err error,
	code codes.Code,
	category runtimev1.RuntimeErrorCode,
) {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != code {
		t.Fatalf("got %v, want %v", err, code)
	}
	for _, detail := range st.Details() {
		if runtimeErr, ok := detail.(*runtimev1.RuntimeError); ok &&
			runtimeErr.GetCode() == category {
			return
		}
	}
	t.Fatalf("missing structured category %v: %v", category, st.Details())
}

func TestDiscoveryModes(t *testing.T) {
	for _, mode := range []string{fake.Normal, fake.IncompatibleVersion, fake.MalformedInfo} {
		t.Run(mode, func(t *testing.T) {
			driver := connect(t, launch(t, t.TempDir(), mode))
			info, err := driver.Info(testContext(t), &runtimev1.InfoRequest{})
			if err != nil {
				t.Fatal(err)
			}
			err = runtimev1.ValidateInfo(info)
			if (err == nil) != (mode == fake.Normal) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestFailureModes(t *testing.T) {
	t.Run(fake.FailPreflight, func(t *testing.T) {
		driver := connect(t, launch(t, t.TempDir(), fake.FailPreflight))
		_, err := driver.Preflight(testContext(t), &runtimev1.PreflightRequest{})
		assertRuntimeError(
			t,
			err,
			codes.Unavailable,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE,
		)
		_, err = driver.ProvisioningPreflight(
			testContext(t),
			&runtimev1.ProvisioningPreflightRequest{},
		)
		assertRuntimeError(
			t,
			err,
			codes.Unavailable,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE,
		)
	})
	t.Run(fake.FailRun, func(t *testing.T) {
		driver := connect(t, launch(t, t.TempDir(), fake.FailRun))
		_, err := driver.RunImage(
			testContext(t),
			&runtimev1.RunImageRequest{WorkspaceId: workspaceID, Image: testImage},
		)
		assertRuntimeError(
			t,
			err,
			codes.Internal,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE,
		)
		assertState(t, driver, "")
	})
	t.Run(fake.NotFound, func(t *testing.T) {
		driver := connect(t, launch(t, t.TempDir(), fake.NotFound))
		createWorkspace(t, driver)
		assertState(t, driver, "")
	})
}

func TestExecModes(t *testing.T) {
	for _, mode := range []string{fake.Normal, fake.ExecEcho, fake.ExecNonzero} {
		t.Run(mode, func(t *testing.T) {
			driver := connect(t, launch(t, t.TempDir(), mode))
			createWorkspace(t, driver)
			stream := startExec(testContext(t), t, driver)
			payload := bytes.Repeat([]byte{0, 255, 128, '\n'}, 32768)
			if err := stream.Send(
				&runtimev1.ExecClientMessage{
					Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: payload},
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(
				&runtimev1.ExecClientMessage{
					Payload: &runtimev1.ExecClientMessage_CloseStdin{
						CloseStdin: &runtimev1.CloseStdin{},
					},
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			assertExecOutput(t, stream, payload, mode)
		})
	}
}

func startExec(
	ctx context.Context,
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
) runtimev1.RuntimeDriver_ExecClient {
	t.Helper()
	stream, err := driver.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{
			WorkspaceId: workspaceID,
			Argv:        []string{"echo", "space argument"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	return stream
}

type execOutput struct {
	stdout, stderr []byte
	exit           *runtimev1.ExecExit
}

func assertExecOutput(
	t *testing.T,
	stream runtimev1.RuntimeDriver_ExecClient,
	payload []byte,
	mode string,
) {
	t.Helper()
	var output execOutput
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		output.accept(t, frame)
	}
	output.verify(t, payload, mode)
}

func (o *execOutput) verify(t *testing.T, payload []byte, mode string) {
	t.Helper()
	if o.exit == nil || !bytes.Equal(o.stdout, payload) {
		t.Fatal("binary output or terminal exit lost")
	}
	var wantCode int32
	var wantStderr string
	if mode == fake.ExecNonzero {
		wantCode, wantStderr = 7, "diagnostic"
	}
	if o.exit.GetExitCode() != wantCode || o.exit.GetSignal() != "" {
		t.Fatal("wrong terminal exit")
	}
	if string(o.stderr) != wantStderr {
		t.Fatal("incorrect stderr")
	}
}

func (o *execOutput) accept(t *testing.T, frame *runtimev1.ExecServerMessage) {
	t.Helper()
	if o.exit != nil {
		t.Fatal("frame after terminal exit")
	}
	switch p := frame.Payload.(type) {
	case *runtimev1.ExecServerMessage_Stdout:
		if len(p.Stdout.GetData()) > 32*1024 {
			t.Fatal("unbounded output frame")
		}
		o.stdout = append(o.stdout, p.Stdout.GetData()...)
	case *runtimev1.ExecServerMessage_Stderr:
		o.stderr = append(o.stderr, p.Stderr.GetData()...)
	case *runtimev1.ExecServerMessage_Exit:
		o.exit = p.Exit
	default:
		t.Fatal("unexpected output frame")
	}
}

func TestSlowExecCancellation(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.ExecSlow))
	createWorkspace(t, driver)
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	stream := startExec(ctx, t, driver)
	cancel()
	_, err := stream.Recv()
	if status.Code(err) != codes.Canceled {
		t.Fatalf("unexpected cancel: %v", err)
	}
}

func TestLogs(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.Logs))
	createWorkspace(t, driver)
	stream, err := driver.Logs(testContext(t), &runtimev1.LogsRequest{WorkspaceId: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, frame.GetData()...)
	}
	if !bytes.Equal(data, []byte("fake stdout\x00\xff\nfake stderr\n")) {
		t.Fatal("logs lost binary bytes")
	}
}

func TestCrashModes(t *testing.T) {
	t.Run(fake.CrashBeforeHandshake, func(t *testing.T) {
		client := launch(t, t.TempDir(), fake.CrashBeforeHandshake)
		if _, err := client.Client(); err == nil {
			t.Fatal("startup crash accepted")
		}
		client.Kill()
		if !client.Exited() {
			t.Fatal("crashed plugin not reaped")
		}
	})
	t.Run(fake.CrashAfterHandshake, func(t *testing.T) {
		client := launch(t, t.TempDir(), fake.CrashAfterHandshake)
		driver := connect(t, client)
		if _, err := driver.Info(testContext(t), &runtimev1.InfoRequest{}); err == nil {
			t.Fatal("Info crash accepted")
		}
		client.Kill()
		if !client.Exited() {
			t.Fatal("crashed plugin not reaped")
		}
	})
	t.Run(fake.ExecCrash, func(t *testing.T) {
		client := launch(t, t.TempDir(), fake.ExecCrash)
		driver := connect(t, client)
		createWorkspace(t, driver)
		stream := startExec(testContext(t), t, driver)
		if _, err := stream.Recv(); err == nil {
			t.Fatal("Exec crash accepted")
		}
		client.Kill()
		if !client.Exited() {
			t.Fatal("crashed plugin not reaped")
		}
	})
}

func TestDelayedHandshakeTimeout(t *testing.T) {
	client := launchWithTimeout(t, t.TempDir(), fake.DelayHandshake, 100*time.Millisecond)
	if _, err := client.Client(); err == nil {
		t.Fatal("delayed startup ignored deadline")
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("delayed plugin not reaped")
	}
}

func TestLifecycleErrorsPreserveState(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.Normal))
	ctx := testContext(t)
	createWorkspace(t, driver)
	_, err := driver.RunImage(
		ctx,
		&runtimev1.RunImageRequest{WorkspaceId: workspaceID, Image: "other"},
	)
	assertRuntimeError(
		t,
		err,
		codes.AlreadyExists,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS,
	)
	assertState(t, driver, "running")
	_, err = driver.Stop(ctx, &runtimev1.StopRequest{})
	assertRuntimeError(
		t,
		err,
		codes.InvalidArgument,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
	)
	_, err = driver.Stop(ctx, &runtimev1.StopRequest{WorkspaceId: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	stream := startExec(ctx, t, driver)
	_, err = stream.Recv()
	assertRuntimeError(
		t,
		err,
		codes.FailedPrecondition,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION,
	)
}
