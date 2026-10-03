package fake

import (
	"io"
	"os"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type execStream = grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage]

func conformanceExec(stream execStream, start *runtimev1.ExecStart) error {
	argv := start.GetArgv()
	if len(argv) == 0 {
		return status.Error(codes.InvalidArgument, "conformance command is required")
	}
	switch argv[0] {
	case "echo", "stderr", "duplex":
		if err := conformanceInput(stream, argv[0]); err != nil {
			return err
		}
		return conformanceExit(stream, &runtimev1.ExecExit{})
	case "arguments":
		return argumentOutput(stream, argv[1:])
	case "error":
		return injectedError(argv)
	case "/bin/sh":
		return shellOutput(stream, argv)
	default:
		return conformanceOutcome(stream, argv[0])
	}
}

func conformanceOutcome(stream execStream, command string) error {
	switch command {
	case "nonzero":
		if err := stream.Send(&runtimev1.ExecServerMessage{
			Payload: &runtimev1.ExecServerMessage_Stderr{
				Stderr: &runtimev1.OutputChunk{Data: []byte("diagnostic")},
			},
		}); err != nil {
			return err
		}
		return conformanceExit(stream, &runtimev1.ExecExit{ExitCode: 7})
	case "signal":
		return conformanceExit(stream, &runtimev1.ExecExit{Signal: "TERM"})
	case "early-exit":
		return conformanceExit(stream, &runtimev1.ExecExit{})
	case "crash":
		return crashExec(stream)
	case "block":
		return blockExec(stream)
	default:
		return status.Error(codes.InvalidArgument, "unknown conformance command")
	}
}

func conformanceInput(stream execStream, command string) error {
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
		data, ok := frame.Payload.(*runtimev1.ExecClientMessage_Stdin)
		if !ok || len(data.Stdin) == 0 {
			return status.Error(codes.InvalidArgument, "expected nonempty stdin or CloseStdin")
		}
		if err := conformanceOutput(stream, command, data.Stdin); err != nil {
			return err
		}
	}
}

func conformanceOutput(stream execStream, command string, data []byte) error {
	for len(data) > 0 {
		size := min(len(data), chunkSize)
		chunk := &runtimev1.OutputChunk{Data: data[:size]}
		if command != "stderr" {
			if err := stream.Send(
				&runtimev1.ExecServerMessage{
					Payload: &runtimev1.ExecServerMessage_Stdout{Stdout: chunk},
				},
			); err != nil {
				return err
			}
		}
		if command != "echo" {
			if err := stream.Send(
				&runtimev1.ExecServerMessage{
					Payload: &runtimev1.ExecServerMessage_Stderr{Stderr: chunk},
				},
			); err != nil {
				return err
			}
		}
		data = data[size:]
	}
	return nil
}

func argumentOutput(stream execStream, argv []string) error {
	var data []byte
	for _, argument := range argv {
		data = append(append(data, []byte(argument)...), 0)
	}
	if err := sendOutput(stream, data); err != nil {
		return err
	}
	return conformanceExit(stream, &runtimev1.ExecExit{})
}

func shellOutput(stream execStream, argv []string) error {
	if len(argv) != 3 || argv[1] != "-c" ||
		argv[2] != "printf 'shell stdout\\n'" {
		return status.Error(codes.InvalidArgument, "expected exact shell scenario argv")
	}
	if err := sendOutput(stream, []byte("shell stdout\n")); err != nil {
		return err
	}
	return conformanceExit(stream, &runtimev1.ExecExit{})
}

func blockExec(stream execStream) error {
	if err := sendOutput(stream, []byte("ready\n")); err != nil {
		return err
	}
	<-stream.Context().Done()
	return status.FromContextError(stream.Context().Err()).Err()
}

func conformanceExit(stream execStream, exit *runtimev1.ExecExit) error {
	return stream.Send(
		&runtimev1.ExecServerMessage{Payload: &runtimev1.ExecServerMessage_Exit{Exit: exit}},
	)
}

func crashExec(stream execStream) error {
	if err := sendOutput(stream, []byte("ready\n")); err != nil {
		return err
	}
	// The client acknowledges receipt before the crash, so readiness cannot be
	// lost in the transport's outbound buffer when the process exits.
	if _, err := stream.Recv(); err != nil {
		return err
	}
	os.Exit(24)
	return nil
}
