package conformance

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func cancellation(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stream := s.block(ctx, t)
	cancel()
	_, err := stream.Recv()
	if status.Code(err) != codes.Canceled {
		t.Fatalf("cancel returned %v", err)
	}
	// Verify the client remains usable after a stream is canceled.
	s.assertState(t, "running")
}

func deadline(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	stream := s.block(ctx, t)
	_, err := stream.Recv()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline returned %v", err)
	}
	s.assertState(t, "running")
}

func (s *session) block(ctx context.Context, t *testing.T) runtimev1.RuntimeDriver_ExecClient {
	t.Helper()
	stream, err := s.client.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendStart(stream, s.command(t, Block)); err != nil {
		t.Fatal(err)
	}
	if err := sendInput(stream, nil); err != nil {
		t.Fatal(err)
	}
	if err := receiveReady(stream); err != nil {
		t.Fatal(err)
	}
	return stream
}

func receiveReady(stream runtimev1.RuntimeDriver_ExecClient) error {
	expected := bytes.NewReader([]byte("ready\n"))
	for expected.Len() > 0 {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := compareChunk(expected, frame.GetStdout().GetData()); err != nil {
			return err
		}
	}
	return nil
}

func malformedFrames(t *testing.T, options Options) {
	cases := []string{
		"missing-start",
		"second-start",
		"empty-data",
		"unset-payload",
		"missing-close",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			s := open(t, options)
			s.create(t)
			stream, err := s.client.Exec(s.ctx)
			if err != nil {
				t.Fatal(err)
			}
			frames := malformedInput(name, s.command(t, Echo))
			for _, frame := range frames {
				// A server can reject before the sender finishes its malformed sequence.
				if err := stream.Send(frame); err != nil {
					break
				}
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("malformed frames returned %v", err)
			}
		})
	}
}

func malformedInput(name string, start *runtimev1.ExecStart) []*runtimev1.ExecClientMessage {
	first := &runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{Start: start}}
	switch name {
	case "missing-start":
		return []*runtimev1.ExecClientMessage{{}}
	case "second-start":
		return []*runtimev1.ExecClientMessage{first, first}
	case "unset-payload":
		return []*runtimev1.ExecClientMessage{first, {}}
	case "empty-data":
		return []*runtimev1.ExecClientMessage{
			first,
			{Payload: &runtimev1.ExecClientMessage_Stdin{}},
		}
	default:
		return []*runtimev1.ExecClientMessage{first}
	}
}
