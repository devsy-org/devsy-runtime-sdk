package plugin_test

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

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devsy-plugin-test-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "runtime fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	cmd := exec.Command("go", "build", "-race", "-o", executable, "../internal/testplugin")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func newClient(t *testing.T, version int) *hplugin.Client {
	t.Helper()
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig:  sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{version: sdkplugin.ClientPlugins()},
		Cmd:              exec.Command(executable),
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		StartTimeout:     10 * time.Second,
	})
	t.Cleanup(client.Kill)
	return client
}

func runtimeClient(t *testing.T) runtimev1.RuntimeDriverClient {
	t.Helper()
	client := newClient(t, sdkplugin.ProtocolVersion)
	rpc, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rpc.Dispense(sdkplugin.Name)
	if err != nil {
		t.Fatal(err)
	}
	return raw.(runtimev1.RuntimeDriverClient)
}

func TestRealPluginInfoAndReap(t *testing.T) {
	client := newClient(t, sdkplugin.ProtocolVersion)
	rpc, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rpc.Dispense(sdkplugin.Name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := raw.(runtimev1.RuntimeDriverClient).Info(ctx, &runtimev1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimev1.ValidateInfo(info); err != nil {
		t.Fatal(err)
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("plugin process has not exited after Kill")
	}
}

func TestIncompatibleHandshake(t *testing.T) {
	client := newClient(t, 2)
	if _, err := client.Client(); err == nil {
		t.Fatal("incompatible protocol accepted")
	}
}

func TestBinaryExecChannelsAndExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := runtimeClient(t).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{Start: &runtimev1.ExecStart{Argv: []string{"echo", "argument with spaces"}}}}); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0, 255, '\n', 128}, 8192)
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: payload}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_CloseStdin{CloseStdin: &runtimev1.CloseStdin{}}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []byte
	exited := false
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if exited {
			t.Fatal("output after exit")
		}
		switch p := frame.Payload.(type) {
		case *runtimev1.ExecServerMessage_Stdout:
			stdout = append(stdout, p.Stdout.Data...)
		case *runtimev1.ExecServerMessage_Stderr:
			stderr = append(stderr, p.Stderr.Data...)
		case *runtimev1.ExecServerMessage_Exit:
			exited = true
			if p.Exit.ExitCode != 7 {
				t.Fatal("exit code lost")
			}
		default:
			t.Fatal("unexpected frame")
		}
	}
	if !bytes.Equal(stdout, payload) || string(stderr) != "diagnostic" || !exited {
		t.Fatal("binary output, diagnostic channel, or terminal exit lost")
	}
}

func TestExecCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := runtimeClient(t).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := stream.Recv(); status.Code(err) != codes.Canceled {
		t.Fatalf("cancel returned %v", err)
	}
}

func TestExecRejectsMalformedFrames(t *testing.T) {
	start := &runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{Start: &runtimev1.ExecStart{}}}
	cases := []struct {
		name   string
		frames []*runtimev1.ExecClientMessage
	}{
		{"missing start", []*runtimev1.ExecClientMessage{{}}},
		{"second start", []*runtimev1.ExecClientMessage{start, start}},
		{"unset payload", []*runtimev1.ExecClientMessage{start, {}}},
		{"empty data frame", []*runtimev1.ExecClientMessage{start, {Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: []byte{}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := runtimeClient(t).Exec(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, frame := range tc.frames {
				if err := stream.Send(frame); err != nil && err != io.EOF {
					t.Fatal(err)
				}
			}
			_, err = stream.Recv()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
		})
	}
}

func TestExecEmptyInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := runtimeClient(t).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []*runtimev1.ExecClientMessage{
		{Payload: &runtimev1.ExecClientMessage_Start{Start: &runtimev1.ExecStart{}}},
		{Payload: &runtimev1.ExecClientMessage_CloseStdin{CloseStdin: &runtimev1.CloseStdin{}}},
	} {
		if err := stream.Send(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	exited := false
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if frame.GetStdout() != nil {
			t.Fatal("unexpected stdout for empty stdin")
		}
		if frame.GetExit() != nil {
			exited = true
		}
	}
	if !exited {
		t.Fatal("empty stdin did not terminate")
	}
}
