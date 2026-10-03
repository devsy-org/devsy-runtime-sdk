// Package conformance runs reusable Runtime Protocol v1 behavior tests.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/protobuf/proto"
)

// Scenario identifies a command with a defined observable behavior.
// See the SDK README for the command and output contract of each scenario.
type Scenario string

// Command scenarios used by Run.
const (
	Echo      Scenario = "echo"
	Arguments Scenario = "arguments"
	Shell     Scenario = "shell"
	Stderr    Scenario = "stderr"
	Duplex    Scenario = "duplex"
	Nonzero   Scenario = "nonzero"
	Signal    Scenario = "signal"
	EarlyExit Scenario = "early-exit"
	Block     Scenario = "block"
)

// Options adapts the suite to a runtime's launcher, image, and test commands.
type Options struct {
	// Connect returns a fresh client per subtest. Register process/connection cleanup
	// with t.Cleanup; it must run after the suite's workspace cleanup.
	Connect func(t *testing.T) runtimev1.RuntimeDriverClient
	// Workspace returns creation intent for a unique, initially absent workspace.
	Workspace func(t *testing.T) *runtimev1.RunImageRequest
	// Command supplies exact argv and execution options for the requested scenario.
	Command func(scenario Scenario, workspaceID string) *runtimev1.ExecStart
	// LogData is the expected finite, merged binary log stream when Logs is advertised.
	LogData []byte
	// Timeout bounds each subtest's RPCs. Zero selects 30 seconds.
	Timeout time.Duration
}

// Run checks discovery, lifecycle, streaming, and context semantics through a real
// client. It never chooses a shell, image, executable, or process ownership policy.
func Run(t *testing.T, options Options) {
	t.Helper()
	if options.Connect == nil || options.Workspace == nil || options.Command == nil {
		t.Fatal("conformance requires Connect, Workspace, and Command adapters")
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.Timeout < 0 {
		t.Fatal("conformance Timeout must be positive")
	}
	tests := []struct {
		name string
		run  func(*testing.T, Options)
	}{
		{"discovery", discovery},
		{"lifecycle", lifecycle},
		{"binary-echo", binaryEcho},
		{"empty-stdin", emptyInput},
		{"arguments", arguments},
		{"shell", shell},
		{"stderr", stderr},
		{"large-duplex", largeDuplex},
		{"nonzero", nonzero},
		{"signal", signal},
		{"early-exit", earlyExit},
		{"cancel", cancellation},
		{"deadline", deadline},
		{"malformed-frames", malformedFrames},
		{"logs", logs},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { test.run(t, options) })
	}
}

type session struct {
	ctx     context.Context
	client  runtimev1.RuntimeDriverClient
	request *runtimev1.RunImageRequest
	info    *runtimev1.InfoResponse
	options Options
}

func open(t *testing.T, options Options) *session {
	t.Helper()
	client := options.Connect(t)
	if client == nil {
		t.Fatal("Connect returned nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), options.Timeout)
	t.Cleanup(cancel)
	request := options.Workspace(t)
	if request == nil || request.GetWorkspaceId() == "" || request.GetImage() == "" {
		t.Fatal("Workspace requires a unique workspace_id and image")
	}
	info, err := client.Info(ctx, &runtimev1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimev1.ValidateInfo(info); err != nil {
		t.Fatal(err)
	}
	return &session{
		ctx: ctx, client: client, request: proto.CloneOf(request),
		info: info, options: options,
	}
}

func (s *session) create(t *testing.T) {
	t.Helper()
	s.assertState(t, "")
	s.preflight(t)
	t.Cleanup(func() {
		// Independent of a canceled operation, but still bounded; runs before client cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), s.options.Timeout)
		defer cancel()
		if _, err := s.client.Delete(
			ctx,
			&runtimev1.DeleteRequest{WorkspaceId: s.request.GetWorkspaceId()},
		); err != nil {
			t.Errorf("workspace cleanup: %v", err)
		}
	})
	if _, err := s.client.RunImage(s.ctx, s.request); err != nil {
		t.Fatal(err)
	}
}

func (s *session) command(t *testing.T, scenario Scenario) *runtimev1.ExecStart {
	t.Helper()
	start := s.options.Command(scenario, s.request.GetWorkspaceId())
	if start == nil || start.GetWorkspaceId() != s.request.GetWorkspaceId() || start.GetTty() {
		t.Fatal("Command requires matching workspace_id and tty=false")
	}
	if len(start.GetArgv()) == 0 {
		t.Fatal("Command requires argv")
	}
	return proto.CloneOf(start)
}

// ArgumentValues returns exact argv test values, including shell metacharacters.
func ArgumentValues() []string {
	return []string{"space argument", "quote'\"", "$HOME;$(echo unwanted)", "", "unicode-λ"}
}
