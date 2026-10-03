package conformance

import (
	"bytes"
	"io"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

type recordedStream struct {
	runtimev1.RuntimeDriver_ExecClient
	frames []*runtimev1.ExecServerMessage
}

func (s *recordedStream) Recv() (*runtimev1.ExecServerMessage, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	frame := s.frames[0]
	s.frames = s.frames[1:]
	return frame, nil
}

func TestStreamValidationRejectsBrokenOutput(t *testing.T) {
	const binaryPayload = "binary\x00"
	output := &runtimev1.ExecServerMessage{Payload: &runtimev1.ExecServerMessage_Stdout{
		Stdout: &runtimev1.OutputChunk{Data: []byte(binaryPayload)},
	}}
	exit := &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{}},
	}
	cases := []struct {
		name     string
		frames   []*runtimev1.ExecServerMessage
		expected string
	}{
		{"missing exit", []*runtimev1.ExecServerMessage{output}, binaryPayload},
		{"truncated output", []*runtimev1.ExecServerMessage{exit}, binaryPayload},
		{"excess output", []*runtimev1.ExecServerMessage{output, exit}, ""},
		{"wrong bytes", []*runtimev1.ExecServerMessage{output, exit}, "wrong!!"},
		{"output after exit", []*runtimev1.ExecServerMessage{exit, output}, binaryPayload},
		{"duplicate exit", []*runtimev1.ExecServerMessage{exit, exit}, ""},
		{"unset payload", []*runtimev1.ExecServerMessage{{}, exit}, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			stream := &recordedStream{frames: test.frames}
			if err := receiveOutput(
				stream,
				outputExpectation{stdout: bytes.NewReader([]byte(test.expected))},
			); err == nil {
				t.Fatal("broken stream accepted")
			}
		})
	}
	if err := receiveOutput(&recordedStream{frames: []*runtimev1.ExecServerMessage{output, exit}},
		outputExpectation{stdout: bytes.NewReader([]byte(binaryPayload))}); err != nil {
		t.Fatal(err)
	}
}

func TestChunkBounds(t *testing.T) {
	for _, size := range []int{0, runtimev1.ChunkSize + 1} {
		data := make([]byte, size)
		if err := compareChunk(bytes.NewReader(data), data); err == nil {
			t.Fatal("invalid chunk size accepted")
		}
	}
}

func TestSignalMeansFailureRegardlessOfExitCode(t *testing.T) {
	for _, code := range []int32{0, 137} {
		exit := &runtimev1.ExecExit{ExitCode: code, Signal: "TERM"}
		if err := checkCompletion(exit, outputExpectation{}); err == nil {
			t.Fatal("signal treated as success")
		}
		if err := checkCompletion(exit, outputExpectation{signal: true}); err != nil {
			t.Fatal(err)
		}
	}
}
