package fake_test

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/conformance"
	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func TestStructuredErrorsAcrossTransport(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.Conformance))
	createWorkspace(t, driver)
	for _, value := range runtimev1.RuntimeErrorCode_value {
		category := runtimev1.RuntimeErrorCode(value)
		t.Run(category.String(), func(t *testing.T) {
			stream, err := driver.Exec(testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(
				&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
					Start: &runtimev1.ExecStart{WorkspaceId: workspaceID, Argv: []string{
						"error", strings.TrimPrefix(category.String(), "RUNTIME_ERROR_CODE_"),
					}},
				}},
			); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			detail := conformance.RequireRuntimeError(t, err, category)
			if detail.GetRuntimeMessage() != "backend diagnostic" ||
				detail.GetDetails()["operation"] != "fixture" {
				t.Fatal("runtime diagnostic metadata lost")
			}
			if detail.GetRetryable() != (category == runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE) {
				t.Fatal("retryability lost")
			}
		})
	}
}
