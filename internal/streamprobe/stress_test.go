package streamprobe_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	pausedInput   = "paused-input"
	slowHost      = "slow-host"
	duplexCommand = "duplex"
)

func TestOwnedDuplex100MiB(t *testing.T) {
	for _, scenario := range []string{duplexCommand, slowHost, pausedInput} {
		t.Run(scenario, func(t *testing.T) { duplex(t, scenario) })
	}
}

func duplex(t *testing.T, scenario string) {
	t.Helper()
	_, driver := launch(t)
	ctx, cancel := context.WithCancel(deadline(t))
	defer cancel()
	command := scenario
	if scenario == slowHost {
		command = duplexCommand
	}
	s := start(ctx, t, driver, command)
	if scenario == pausedInput {
		ready(t, s)
	}
	done := sender(s)
	defer func() { cancel(); _ = joined(t, done) }()
	if scenario == pausedInput {
		release(t, driver)
	}
	if err := receive(s, scenario == slowHost); err != nil {
		t.Fatal(err)
	}
	if err := joined(t, done); err != nil {
		t.Fatal(err)
	}
}

func ready(t *testing.T, s runtimev1.RuntimeDriver_ExecClient) {
	t.Helper()
	frame, err := s.Recv()
	if err != nil || string(frame.GetStdout().GetData()) != "ready" {
		t.Fatalf("readiness: %v", err)
	}
}

func release(t *testing.T, driver runtimev1.RuntimeDriverClient) {
	t.Helper()
	s := start(deadline(t), t, driver, "release")
	if err := s.CloseSend(); err != nil {
		t.Fatal(err)
	}
	frame, err := s.Recv()
	if err != nil || frame.GetExit() == nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := s.Recv(); err != io.EOF {
		t.Fatalf("release completion: %v", err)
	}
}

func TestOwnedBackpressureCancellationBoundsRetainedHeap(t *testing.T) {
	for _, command := range []string{pausedInput, duplexCommand} {
		t.Run(command, func(t *testing.T) { backpressure(t, command) })
	}
}

func backpressure(t *testing.T, command string) {
	t.Helper()
	_, driver := launch(t)
	pluginBefore, hostBefore := memory(t, driver), hostMemory()
	ctx, cancel := context.WithCancel(deadline(t))
	defer cancel()
	s := start(ctx, t, driver, command)
	prime(t, s, command)
	done := sender(s)
	defer func() { cancel(); _ = joined(t, done) }()
	awaitSender(t, done)
	pluginDuring, hostDuring := memory(t, driver), hostMemory()
	assertHeapBudget(t, [2]uint64{hostBefore, hostDuring}, [2]uint64{pluginBefore, pluginDuring})
	select {
	case <-done.done:
		t.Fatalf("sender bypassed paused consumer: %v", done.err)
	default:
	}
	cancel()
	canceled(t, s)
	if err := joined(t, done); err == nil {
		t.Fatal("blocked stdin completed after cancellation")
	}
	_ = memory(t, driver)
}

func prime(t *testing.T, s runtimev1.RuntimeDriver_ExecClient, command string) {
	t.Helper()
	if command == pausedInput {
		ready(t, s)
	}
	if err := s.Send(
		&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: pattern()}},
	); err != nil {
		t.Fatal(err)
	}
	if command != pausedInput {
		frame, err := s.Recv()
		if err != nil || !bytes.Equal(frame.GetStdout().GetData(), pattern()) {
			t.Fatalf("initial output: %v", err)
		}
	}
}

func assertHeapBudget(t *testing.T, host, plugin [2]uint64) {
	t.Helper()
	const budget = 32 << 20
	t.Logf(
		"retained heap baseline/paused: host=%d/%d plugin=%d/%d budget=%d",
		host[0],
		host[1],
		plugin[0],
		plugin[1],
		budget,
	)
	if plugin[1] > plugin[0]+budget || host[1] > host[0]+budget {
		t.Fatal("paused stream retained more than the 32 MiB heap-growth budget")
	}
}

func TestOwnedStreamEarlyExitAndCrashJoinSender(t *testing.T) {
	for _, command := range []string{"early-exit", "crash"} {
		t.Run(command, func(t *testing.T) { interrupted(t, command) })
	}
}

func interrupted(t *testing.T, command string) {
	t.Helper()
	client, driver := launch(t)
	ctx, cancel := context.WithCancel(deadline(t))
	defer cancel()
	s := start(ctx, t, driver, command)
	ready(t, s)
	done := sender(s)
	defer func() { cancel(); _ = joined(t, done) }()
	assertInterrupted(t, s, command)
	if err := joined(t, done); err == nil {
		t.Fatal("stdin completed despite command interruption")
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("supervisor was not reaped")
	}
}

func canceled(t *testing.T, s runtimev1.RuntimeDriver_ExecClient) {
	t.Helper()
	for {
		_, err := s.Recv()
		if err == nil {
			continue
		}
		if status.Code(err) != codes.Canceled {
			t.Fatalf("cancel: %v", err)
		}
		return
	}
}

func assertInterrupted(t *testing.T, s runtimev1.RuntimeDriver_ExecClient, command string) {
	t.Helper()
	frame, err := s.Recv()
	if command == "crash" {
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("crash: %v", err)
		}
	} else {
		if err != nil || frame.GetExit() == nil || frame.GetExit().GetExitCode() != 0 {
			t.Fatalf("early exit: %v (%v)", frame, err)
		}
		if _, err := s.Recv(); err != io.EOF {
			t.Fatalf("early completion: %v", err)
		}
	}
}
