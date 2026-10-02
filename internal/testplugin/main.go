// Test fixture only: proves the real plugin transport, not runtime conformance.
package main

import (
	"context"
	"io"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fixture struct {
	runtimev1.UnimplementedRuntimeDriverServer
}

func (*fixture) Info(context.Context, *runtimev1.InfoRequest) (*runtimev1.InfoResponse, error) {
	return &runtimev1.InfoResponse{
		ApiMajor:      runtimev1.APIMajor,
		DriverName:    "transport-fixture",
		DriverVersion: "0.0.0",
		RuntimeName:   "fake",
		Capabilities: &runtimev1.Capabilities{
			RecreateMode: runtimev1.RecreateMode_RECREATE_MODE_STOP,
		},
	}, nil
}

func (*fixture) Exec(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetStart() == nil {
		return status.Error(codes.InvalidArgument, "first Exec frame must be start")
	}
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return status.Error(codes.InvalidArgument, "CloseStdin is required")
		}
		if err != nil {
			return err
		}
		if _, ok := frame.Payload.(*runtimev1.ExecClientMessage_CloseStdin); ok {
			return sendExit(stream)
		}
		if err := echoStdin(stream, frame); err != nil {
			return err
		}
	}
}

func echoStdin(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
	frame *runtimev1.ExecClientMessage,
) error {
	payload, ok := frame.Payload.(*runtimev1.ExecClientMessage_Stdin)
	if !ok {
		return status.Error(codes.InvalidArgument, "unexpected Exec frame")
	}
	if len(payload.Stdin) == 0 {
		return status.Error(codes.InvalidArgument, "stdin data frames must be nonempty")
	}
	return stream.Send(&runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Stdout{
			Stdout: &runtimev1.OutputChunk{Data: payload.Stdin},
		},
	})
}

func sendExit(
	stream grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	if err := stream.Send(&runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Stderr{
			Stderr: &runtimev1.OutputChunk{Data: []byte("diagnostic")},
		},
	}); err != nil {
		return err
	}
	return stream.Send(&runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{ExitCode: 7}},
	})
}

func main() { server.Serve(&fixture{}) }
