package fake_test

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func TestMidStreamCrashPreservesWorkspace(t *testing.T) {
	dir := t.TempDir()
	process := launch(t, dir, fake.Conformance)
	driver := connect(t, process)
	createWorkspace(t, driver)
	stream, err := driver.Exec(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{WorkspaceId: workspaceID, Argv: []string{"crash"}},
	}}); err != nil {
		t.Fatal(err)
	}
	ready, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ready.GetStdout().GetData(), []byte("ready\n")) {
		t.Fatal("missing crash readiness")
	}
	if err := stream.Send(
		&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: []byte{1}}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("crash appeared to succeed")
	}
	process.Kill()
	if !process.Exited() {
		t.Fatal("crashed process not reaped")
	}
	restarted := connect(t, launch(t, dir, fake.Normal))
	assertState(t, restarted, "running")
}
