// Stream fixture exercises transport flow control without buffering whole payloads.
package main

import (
	"encoding/json"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stream = grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage]

type fixture struct {
	runtimev1.UnimplementedRuntimeDriverServer
	gate    chan struct{}
	release sync.Once
}

func (f *fixture) Exec(s stream) error {
	first, err := s.Recv()
	if err != nil {
		return err
	}
	if first.GetStart() == nil || len(first.GetStart().GetArgv()) != 1 {
		return status.Error(codes.InvalidArgument, "expected fixture command")
	}
	return f.run(s, first.GetStart().GetArgv()[0])
}

func (f *fixture) run(s stream, command string) error {
	switch command {
	case "memory":
		return memory(s)
	case "release":
		f.release.Do(func() { close(f.gate) })
		return exit(s, 0)
	case "early-exit", "crash":
		return interrupt(s, command)
	case "paused-input":
		return f.paused(s)
	case "duplex":
		return duplex(s, false)
	default:
		return status.Error(codes.InvalidArgument, "unknown fixture command")
	}
}

func duplex(s stream, slow bool) error {
	var transferred int
	for {
		message, err := s.Recv()
		if err != nil {
			return err
		}
		if message.GetCloseStdin() != nil {
			return tail(s)
		}
		data := message.GetStdin()
		if err := echo(s, data); err != nil {
			return err
		}
		transferred += len(data)
		if slow && transferred%(1<<20) == 0 {
			time.Sleep(time.Millisecond)
		}
	}
}

func tail(s stream) error {
	if err := output(s, []byte("stdout tail\x00\xff"), false); err != nil {
		return err
	}
	if err := output(s, []byte("stderr tail\xff\x00"), true); err != nil {
		return err
	}
	return exit(s, 7)
}

func interrupt(s stream, command string) error {
	if err := output(s, []byte("ready"), false); err != nil {
		return err
	}
	// A client data frame proves the sender is active before exit or crash.
	if _, err := s.Recv(); err != nil {
		return err
	}
	if command == "crash" {
		os.Exit(24)
	}
	return exit(s, 0)
}

func output(s stream, data []byte, stderr bool) error {
	chunk := &runtimev1.OutputChunk{Data: data}
	message := &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Stdout{Stdout: chunk},
	}
	if stderr {
		message.Payload = &runtimev1.ExecServerMessage_Stderr{Stderr: chunk}
	}
	return s.Send(message)
}

func exit(s stream, code int32) error {
	return s.Send(&runtimev1.ExecServerMessage{Payload: &runtimev1.ExecServerMessage_Exit{
		Exit: &runtimev1.ExecExit{ExitCode: code},
	}})
}

func main() { server.Serve(&fixture{gate: make(chan struct{})}) }

func memory(s stream) error {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	data, err := json.Marshal(stats.HeapAlloc)
	if err != nil {
		return err
	}
	if err := output(s, data, false); err != nil {
		return err
	}
	return exit(s, 0)
}

func (f *fixture) paused(s stream) error {
	if err := output(s, []byte("ready"), false); err != nil {
		return err
	}
	select {
	case <-f.gate:
		return duplex(s, true)
	case <-s.Context().Done():
		return status.FromContextError(s.Context().Err()).Err()
	}
}

func echo(s stream, data []byte) error {
	if len(data) == 0 || len(data) > runtimev1.ChunkSize {
		return status.Error(codes.InvalidArgument, "expected bounded stdin")
	}
	if err := output(s, data, false); err != nil {
		return err
	}
	return output(s, data, true)
}
