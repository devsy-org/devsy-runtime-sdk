package fake_test

import (
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/conformance"
	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func TestProtocolConformance(t *testing.T) {
	conformance.Run(t, conformance.Options{
		Connect: func(t *testing.T) runtimev1.RuntimeDriverClient {
			process := launch(t, t.TempDir(), fake.Conformance)
			t.Cleanup(func() {
				process.Kill()
				if !process.Exited() {
					t.Error("conformance plugin was not reaped")
				}
			})
			return connect(t, process)
		},
		Workspace: func(t *testing.T) *runtimev1.RunImageRequest {
			return &runtimev1.RunImageRequest{WorkspaceId: t.Name(), Image: "fake:image"}
		},
		Command: conformanceCommand,
		LogData: []byte("fake stdout\x00\xff\nfake stderr\n"),
	})
}

func conformanceCommand(scenario conformance.Scenario, workspaceID string) *runtimev1.ExecStart {
	argv := []string{string(scenario)}
	if scenario == conformance.Arguments {
		argv = append(argv, conformance.ArgumentValues()...)
	}
	if scenario == conformance.Shell {
		argv = []string{"/bin/sh", "-c", "printf 'shell stdout\\n'"}
	}
	return &runtimev1.ExecStart{WorkspaceId: workspaceID, Argv: argv}
}
