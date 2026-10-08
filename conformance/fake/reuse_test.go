package fake_test

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestReusePreflightPreservesWorkspace(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.Normal))
	createWorkspace(t, driver)
	before, err := driver.Find(testContext(t), &runtimev1.FindRequest{WorkspaceId: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id, user string
		code           codes.Code
		category       runtimev1.RuntimeErrorCode
	}{
		{name: "same identity", id: workspaceID, user: "1000", code: codes.OK},
		{
			name: "changed identity", id: workspaceID, user: "root", code: codes.FailedPrecondition,
			category: runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION,
		},
		{
			name: "missing identity", id: workspaceID, code: codes.InvalidArgument,
			category: runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
		},
		{
			name: "missing workspace", id: "absent", user: "1000", code: codes.NotFound,
			category: runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND,
		},
		{
			name: "missing workspace id", user: "1000", code: codes.InvalidArgument,
			category: runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := driver.ReusePreflight(testContext(t), &runtimev1.ReusePreflightRequest{
				WorkspaceId: tc.id, RemoteUser: tc.user,
			})
			if tc.code == codes.OK {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertRuntimeError(t, err, tc.code, tc.category)
			}
			after, err := driver.Find(
				testContext(t),
				&runtimev1.FindRequest{WorkspaceId: workspaceID},
			)
			if err != nil || !proto.Equal(before, after) {
				t.Fatalf("reuse preflight changed workspace: %v, %v", after, err)
			}
		})
	}
}

func TestReusePreflightCancellation(t *testing.T) {
	driver := connect(t, launch(t, t.TempDir(), fake.Normal))
	createWorkspace(t, driver)
	ctx, cancel := context.WithCancel(testContext(t))
	cancel()
	_, err := driver.ReusePreflight(ctx, &runtimev1.ReusePreflightRequest{
		WorkspaceId: workspaceID, RemoteUser: "1000",
	})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("canceled preflight returned %v", err)
	}
	assertState(t, driver, "running")
}
