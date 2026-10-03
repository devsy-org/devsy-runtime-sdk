package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

type outputExpectation struct {
	stdout  io.Reader
	stderr  io.Reader
	nonzero bool
	signal  bool
}

func (s *session) exec(
	t *testing.T,
	scenario Scenario,
	input io.Reader,
	expected outputExpectation,
) {
	t.Helper()
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stream, err := s.client.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendStart(stream, s.command(t, scenario)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sendInput(stream, input) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	if err := receiveOutput(stream, expected); err != nil {
		t.Fatal(err)
	}
	err = <-done
	joined = true
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func sendStart(stream runtimev1.RuntimeDriver_ExecClient, start *runtimev1.ExecStart) error {
	return stream.Send(&runtimev1.ExecClientMessage{
		Payload: &runtimev1.ExecClientMessage_Start{Start: start},
	})
}

func sendInput(stream runtimev1.RuntimeDriver_ExecClient, input io.Reader) error {
	if input != nil {
		if err := sendData(stream, input); err != nil {
			return err
		}
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{
		Payload: &runtimev1.ExecClientMessage_CloseStdin{CloseStdin: &runtimev1.CloseStdin{}},
	}); err != nil {
		return err
	}
	return stream.CloseSend()
}

func sendData(stream runtimev1.RuntimeDriver_ExecClient, input io.Reader) error {
	buffer := make([]byte, runtimev1.ChunkSize)
	for {
		n, err := input.Read(buffer)
		if n > 0 {
			if sendErr := stream.Send(&runtimev1.ExecClientMessage{
				Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: buffer[:n]},
			}); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

func receiveOutput(stream runtimev1.RuntimeDriver_ExecClient, expected outputExpectation) error {
	var exit *runtimev1.ExecExit
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if exit != nil {
			return errors.New("output received after terminal exit")
		}
		if terminal := frame.GetExit(); terminal != nil {
			exit = terminal
			continue
		}
		if err := checkOutput(frame, expected); err != nil {
			return err
		}
	}
	return checkCompletion(exit, expected)
}

func checkOutput(frame *runtimev1.ExecServerMessage, expected outputExpectation) error {
	switch p := frame.Payload.(type) {
	case *runtimev1.ExecServerMessage_Stdout:
		return compareChunk(expected.stdout, p.Stdout.GetData())
	case *runtimev1.ExecServerMessage_Stderr:
		return compareChunk(expected.stderr, p.Stderr.GetData())
	default:
		return errors.New("unexpected Exec frame")
	}
}

func compareChunk(expected io.Reader, data []byte) error {
	if len(data) == 0 || len(data) > runtimev1.ChunkSize {
		return errors.New("output frame must contain 1..32768 bytes")
	}
	if expected == nil {
		return errors.New("unexpected output channel")
	}
	want := make([]byte, len(data))
	if _, err := io.ReadFull(expected, want); err != nil {
		return fmt.Errorf("excess output: %w", err)
	}
	if !bytes.Equal(data, want) {
		return errors.New("output bytes differ")
	}
	return nil
}

func checkCompletion(exit *runtimev1.ExecExit, expected outputExpectation) error {
	if exit == nil {
		return errors.New("terminal exit frame missing")
	}
	if (exit.GetSignal() != "") != expected.signal {
		return fmt.Errorf("unexpected exit signal: %v", exit)
	}
	if !expected.signal && (exit.GetExitCode() != 0) != expected.nonzero {
		return fmt.Errorf("unexpected terminal exit: %v", exit)
	}
	for _, reader := range []io.Reader{expected.stdout, expected.stderr} {
		if reader == nil {
			continue
		}
		if _, err := io.ReadFull(reader, make([]byte, 1)); err != io.EOF {
			return errors.New("output truncated before terminal exit")
		}
	}
	return nil
}
