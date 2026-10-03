package conformance

import (
	"bytes"
	"io"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func logs(t *testing.T, options Options) {
	s := open(t, options)
	if !s.info.GetCapabilities().GetLogs() {
		t.Skip("runtime does not advertise Logs")
	}
	if options.LogData == nil {
		t.Fatal("LogData is required when Logs is advertised")
	}
	s.create(t)
	stream, err := s.client.Logs(
		s.ctx,
		&runtimev1.LogsRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiveLogs(stream, options.LogData); err != nil {
		t.Fatal(err)
	}
}

func receiveLogs(stream runtimev1.RuntimeDriver_LogsClient, data []byte) error {
	expected := bytes.NewReader(data)
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := compareChunk(expected, chunk.GetData()); err != nil {
			return err
		}
	}
	if expected.Len() != 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}
