package fake

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	running = "running"
	stopped = "stopped"
)

// Find returns ordinary absence without a gRPC error.
func (d *Driver) Find(
	ctx context.Context,
	req *runtimev1.FindRequest,
) (*runtimev1.FindResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateWorkspace(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if d.config.Mode == NotFound {
		return &runtimev1.FindResponse{}, nil
	}
	container, err := d.load(req.GetWorkspaceId())
	if err != nil {
		return nil, storageError()
	}
	return &runtimev1.FindResponse{Found: container != nil, Container: container}, nil
}

// RunImage creates a running workspace and rejects duplicate creation.
func (d *Driver) RunImage(
	ctx context.Context,
	req *runtimev1.RunImageRequest,
) (*runtimev1.RunImageResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateWorkspace(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if req.GetImage() == "" {
		return nil, runtimeError(
			codes.InvalidArgument,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
			"image is required",
			false,
		)
	}
	if d.config.Mode == FailRun {
		return nil, runtimeError(
			codes.Internal,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE,
			"fake image creation failed",
			false,
		)
	}
	container, err := d.load(req.GetWorkspaceId())
	if err != nil {
		return nil, storageError()
	}
	if container != nil {
		return nil, runtimeError(
			codes.AlreadyExists,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS,
			"workspace already exists",
			false,
		)
	}
	container = newContainer(req)
	if err := d.save(container); err != nil {
		return nil, storageError()
	}
	return &runtimev1.RunImageResponse{}, nil
}

// Start normalizes an already-running workspace to success.
func (d *Driver) Start(
	ctx context.Context,
	req *runtimev1.StartRequest,
) (*runtimev1.StartResponse, error) {
	if err := d.transition(ctx, req.GetWorkspaceId(), running); err != nil {
		return nil, err
	}
	return &runtimev1.StartResponse{}, nil
}

// Stop normalizes an already-stopped workspace to success.
func (d *Driver) Stop(
	ctx context.Context,
	req *runtimev1.StopRequest,
) (*runtimev1.StopResponse, error) {
	if err := d.transition(ctx, req.GetWorkspaceId(), stopped); err != nil {
		return nil, err
	}
	return &runtimev1.StopResponse{}, nil
}

// Delete succeeds even when the workspace is already absent.
func (d *Driver) Delete(
	ctx context.Context,
	req *runtimev1.DeleteRequest,
) (*runtimev1.DeleteResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateWorkspace(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	err := os.Remove(d.statePath(req.GetWorkspaceId()))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, storageError()
	}
	return &runtimev1.DeleteResponse{}, nil
}

func (d *Driver) transition(ctx context.Context, id, state string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateWorkspace(ctx, id); err != nil {
		return err
	}
	container, err := d.load(id)
	if err != nil {
		return storageError()
	}
	if container == nil {
		return missingWorkspace()
	}
	if container.GetState().GetStatus() == state {
		return nil
	}
	container.State.Status = state
	if state == running {
		container.State.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := d.save(container); err != nil {
		return storageError()
	}
	return nil
}

func newContainer(req *runtimev1.RunImageRequest) *runtimev1.ContainerDetails {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	container := &runtimev1.ContainerDetails{
		Id: req.GetWorkspaceId(), CreatedAt: now,
		State:  &runtimev1.ContainerState{Status: running, StartedAt: now},
		Config: &runtimev1.ContainerConfig{User: req.GetUser(), Labels: make(map[string]string)},
	}
	for _, label := range req.GetLabels() {
		key, value, _ := strings.Cut(label, "=")
		container.Config.Labels[key] = value
	}
	mounts := append([]*runtimev1.Mount{}, req.GetMounts()...)
	if req.GetWorkspaceMount() != nil {
		mounts = append(mounts, req.GetWorkspaceMount())
	}
	for _, mount := range mounts {
		container.Mounts = append(container.Mounts, &runtimev1.ContainerMount{
			Type:   strings.TrimPrefix(strings.ToLower(mount.GetType().String()), "mount_type_"),
			Source: mount.GetSource(), Destination: mount.GetTarget(),
		})
	}
	return container
}

func validateWorkspace(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if id == "" {
		return runtimeError(
			codes.InvalidArgument,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
			"workspace_id is required",
			false,
		)
	}
	return nil
}

func missingWorkspace() error {
	return runtimeError(codes.NotFound, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND,
		"workspace does not exist", false)
}

func storageError() error {
	return runtimeError(
		codes.Internal,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE,
		"fake runtime state could not be read or written",
		false,
	)
}
