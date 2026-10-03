package fake

import (
	"context"
	"io"
	"os"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const chunkSize = 32 * 1024

// Exec echoes stdin in bounded frames, or simulates a selected execution failure.
func (d *Driver) Exec(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first Exec frame must be start")
	}
	if err := d.requireRunning(stream.Context(), start.GetWorkspaceId()); err != nil {
		return err
	}
	if start.GetTty() {
		return status.Error(codes.Unimplemented, "fake runtime does not support TTY")
	}
	if d.config.Mode == ExecCrash {
		os.Exit(24)
	}
	if d.config.Mode == ExecSlow {
		// Cancellation is the readiness-independent trigger; no timing assumptions are required.
		<-stream.Context().Done()
		return status.FromContextError(stream.Context().Err()).Err()
	}
	if err := echoInput(stream); err != nil {
		return err
	}
	return d.sendExit(stream)
}

// Logs returns deterministic merged binary output in bounded frames.
func (d *Driver) Logs(
	req *runtimev1.LogsRequest,
	stream grpc.ServerStreamingServer[runtimev1.OutputChunk],
) error {
	if err := d.requireRunning(stream.Context(), req.GetWorkspaceId()); err != nil {
		return err
	}
	for _, data := range [][]byte{[]byte("fake stdout\x00\xff\n"), []byte("fake stderr\n")} {
		if err := stream.Send(&runtimev1.OutputChunk{Data: data}); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) requireRunning(ctx context.Context, id string) error {
	found, err := d.Find(ctx, &runtimev1.FindRequest{WorkspaceId: id})
	if err != nil {
		return err
	}
	if !found.GetFound() {
		return missingWorkspace()
	}
	if found.GetContainer().GetState().GetStatus() != running {
		return runtimeError(codes.FailedPrecondition,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION,
			"workspace is not running", false)
	}
	return nil
}

func echoInput(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return status.Error(codes.InvalidArgument, "CloseStdin is required")
		}
		if err != nil {
			return err
		}
		if _, ok := frame.Payload.(*runtimev1.ExecClientMessage_CloseStdin); ok {
			return nil
		}
		payload, ok := frame.Payload.(*runtimev1.ExecClientMessage_Stdin)
		if !ok || len(payload.Stdin) == 0 {
			return status.Error(codes.InvalidArgument, "expected nonempty stdin or CloseStdin")
		}
		if err := sendOutput(stream, payload.Stdin); err != nil {
			return err
		}
	}
}

func sendOutput(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
	data []byte,
) error {
	for len(data) > 0 {
		size := min(len(data), chunkSize)
		if err := stream.Send(&runtimev1.ExecServerMessage{
			Payload: &runtimev1.ExecServerMessage_Stdout{
				Stdout: &runtimev1.OutputChunk{Data: data[:size]},
			},
		}); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

func (d *Driver) sendExit(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	var code int32
	if d.config.Mode == ExecNonzero {
		code = 7
		if err := stream.Send(&runtimev1.ExecServerMessage{
			Payload: &runtimev1.ExecServerMessage_Stderr{
				Stderr: &runtimev1.OutputChunk{Data: []byte("diagnostic")},
			},
		}); err != nil {
			return err
		}
	}
	return stream.Send(&runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{ExitCode: code}},
	})
}
