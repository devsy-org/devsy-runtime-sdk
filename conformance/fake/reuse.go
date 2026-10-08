package fake

import (
	"context"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
)

// ReusePreflight checks the saved developer identity without changing resource state.
func (d *Driver) ReusePreflight(
	ctx context.Context,
	req *runtimev1.ReusePreflightRequest,
) (*runtimev1.ReusePreflightResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateWorkspace(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if req.GetRemoteUser() == "" {
		return nil, runtimeError(codes.InvalidArgument,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
			"remote_user is required", false)
	}
	container, err := d.load(req.GetWorkspaceId())
	if err != nil {
		return nil, storageError()
	}
	if container == nil {
		return nil, missingWorkspace()
	}
	if container.GetConfig().GetLabels()[remoteUserLabel] != req.GetRemoteUser() {
		return nil, runtimeError(codes.FailedPrecondition,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION,
			"workspace developer identity changed; rerun with --recreate", false)
	}
	return &runtimev1.ReusePreflightResponse{}, nil
}

func effectiveRemoteUser(req *runtimev1.RunImageRequest) string {
	if req.GetRemoteUser() != "" {
		return req.GetRemoteUser()
	}
	if req.GetUser() != "" {
		return req.GetUser()
	}
	return "root"
}
