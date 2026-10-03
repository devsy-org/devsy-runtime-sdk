package streamprobe_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

const trafficBytes = 100 << 20

func pattern() []byte {
	data := make([]byte, runtimev1.ChunkSize)
	for i := range data {
		data[i] = byte(i*31 + i/256)
	}
	return data
}

func send(s runtimev1.RuntimeDriver_ExecClient, started chan struct{}) error {
	data := pattern()
	for remaining := trafficBytes; remaining > 0; remaining -= len(data) {
		if err := s.Send(
			&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: data}},
		); err != nil {
			return err
		}
		if remaining == trafficBytes {
			close(started)
		}
	}
	if err := s.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_CloseStdin{
		CloseStdin: &runtimev1.CloseStdin{},
	}}); err != nil {
		return err
	}
	return s.CloseSend()
}

type upload struct {
	done    chan struct{}
	started chan struct{}
	err     error
}

func sender(s runtimev1.RuntimeDriver_ExecClient) *upload {
	pending := &upload{done: make(chan struct{}), started: make(chan struct{})}
	go func() { pending.err = send(s, pending.started); close(pending.done) }()
	return pending
}

func joined(t *testing.T, pending *upload) error {
	t.Helper()
	select {
	case <-pending.done:
		return pending.err
	case <-time.After(10 * time.Second):
		t.Fatal("stream sender did not stop")
		return nil
	}
}

type output struct {
	stdout, stderr     hash.Hash
	outBytes, errBytes int
	exited             bool
	slow               bool
}

func receive(s runtimev1.RuntimeDriver_ExecClient, slow bool) error {
	result := output{stdout: sha256.New(), stderr: sha256.New(), slow: slow}
	for {
		message, err := s.Recv()
		if err == io.EOF {
			return result.verify()
		}
		if err != nil {
			return err
		}
		if err := result.accept(message); err != nil {
			return err
		}
	}
}

func (o *output) accept(message *runtimev1.ExecServerMessage) error {
	if o.exited {
		return errors.New("frame after terminal exit")
	}
	return o.channel(message)
}

func (o *output) channel(message *runtimev1.ExecServerMessage) error {
	switch payload := message.Payload.(type) {
	case *runtimev1.ExecServerMessage_Exit:
		return o.terminal(payload.Exit)
	case *runtimev1.ExecServerMessage_Stdout:
		return o.writeStdout(payload.Stdout.GetData())
	case *runtimev1.ExecServerMessage_Stderr:
		if err := digest(o.stderr, payload.Stderr.GetData()); err != nil {
			return err
		}
		o.errBytes += len(payload.Stderr.GetData())
	default:
		return errors.New("unexpected output")
	}
	return nil
}

func (o *output) terminal(exit *runtimev1.ExecExit) error {
	if exit.GetExitCode() != 7 || exit.GetSignal() != "" {
		return errors.New("wrong exit")
	}
	o.exited = true
	return nil
}

func (o *output) writeStdout(data []byte) error {
	if err := digest(o.stdout, data); err != nil {
		return err
	}
	o.outBytes += len(data)
	if o.slow && o.outBytes%(1<<20) == 0 {
		time.Sleep(time.Millisecond)
	}
	return nil
}

func digest(h hash.Hash, data []byte) error {
	if len(data) == 0 || len(data) > runtimev1.ChunkSize {
		return errors.New("unbounded output frame")
	}
	_, err := h.Write(data)
	return err
}

func (o *output) verify() error {
	if !o.exited {
		return errors.New("missing terminal exit")
	}
	for _, channel := range []struct {
		got  hash.Hash
		size int
		tail string
	}{
		{o.stdout, o.outBytes, "stdout tail\x00\xff"}, {o.stderr, o.errBytes, "stderr tail\xff\x00"},
	} {
		want := sha256.New()
		data := pattern()
		for remaining := trafficBytes; remaining > 0; remaining -= len(data) {
			_, _ = want.Write(data)
		}
		_, _ = want.Write([]byte(channel.tail))
		if channel.size != trafficBytes+len(channel.tail) ||
			!bytes.Equal(channel.got.Sum(nil), want.Sum(nil)) {
			return fmt.Errorf("binary output or tail lost: %d bytes", channel.size)
		}
	}
	return nil
}

func memory(t *testing.T, driver runtimev1.RuntimeDriverClient) uint64 {
	t.Helper()
	s := start(deadline(t), t, driver, "memory")
	if err := s.CloseSend(); err != nil {
		t.Fatal(err)
	}
	frame, err := s.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var heap uint64
	if err := json.Unmarshal(frame.GetStdout().GetData(), &heap); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Recv()
	if err != nil || terminal.GetExit() == nil {
		t.Fatalf("missing memory exit: %v", err)
	}
	if _, err := s.Recv(); err != io.EOF {
		t.Fatalf("memory stream: %v", err)
	}
	return heap
}

func hostMemory() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func awaitSender(t *testing.T, pending *upload) {
	t.Helper()
	select {
	case <-pending.started:
	case <-pending.done:
		t.Fatalf("sender stopped before first data frame: %v", pending.err)
	case <-time.After(10 * time.Second):
		t.Fatal("sender did not start")
	}
}
