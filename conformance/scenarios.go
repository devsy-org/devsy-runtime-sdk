package conformance

import (
	"bytes"
	"io"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func binaryEcho(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	data := []byte{0, 255, 128, '\n', '\r', 1}
	s.exec(t, Echo, bytes.NewReader(data), outputExpectation{stdout: bytes.NewReader(data)})
}

func emptyInput(t *testing.T, options Options) {
	inputs := []struct {
		name  string
		input io.Reader
	}{
		{"nil", nil}, {"empty", bytes.NewReader(nil)},
	}
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			s := open(t, options)
			s.create(t)
			s.exec(t, Echo, input.input, outputExpectation{})
		})
	}
}

func arguments(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	data := []byte{}
	for _, argument := range ArgumentValues() {
		data = append(append(data, []byte(argument)...), 0)
	}
	s.exec(t, Arguments, nil, outputExpectation{stdout: bytes.NewReader(data)})
}

func shell(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	s.exec(t, Shell, nil, outputExpectation{stdout: bytes.NewReader([]byte("shell stdout\n"))})
}

func stderr(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	data := []byte{0, 255, 128, '\n'}
	s.exec(t, Stderr, bytes.NewReader(data), outputExpectation{stderr: bytes.NewReader(data)})
}

func largeDuplex(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	s.exec(t, Duplex, patternReader(), outputExpectation{
		stdout: &gatedReader{reader: patternReader(), ready: func() {
			// Hold the output consumer until an independent RPC completes. The stream
			// must tolerate backpressure without preventing other operations.
			if _, err := s.client.Info(s.ctx, &runtimev1.InfoRequest{}); err != nil {
				t.Error(err)
			}
		}},
		stderr: patternReader(),
	})
}

func nonzero(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	s.exec(
		t,
		Nonzero,
		nil,
		outputExpectation{stderr: bytes.NewReader([]byte("diagnostic")), nonzero: true},
	)
}

func signal(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	s.exec(t, Signal, nil, outputExpectation{signal: true})
}

func earlyExit(t *testing.T, options Options) {
	s := open(t, options)
	s.create(t)
	s.exec(t, EarlyExit, patternReader(), outputExpectation{})
}

type repeatingReader struct {
	remaining int
	next      byte
}

func patternReader() io.Reader { return &repeatingReader{remaining: 12 * 1024 * 1024} }

func (r *repeatingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range n {
		p[i] = r.next
		r.next++
	}
	r.remaining -= n
	return n, nil
}

type gatedReader struct {
	reader io.Reader
	ready  func()
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.ready != nil {
		r.ready()
		r.ready = nil
	}
	return r.reader.Read(p)
}
