// Package fake provides a persistent runtime fixture for real protocol tests.
// A state directory must have only one active Driver owner at a time.
package fake

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Modes are deterministic failure and streaming scenarios selected at startup.
const (
	Normal               = "normal"
	CrashBeforeHandshake = "crash-before-handshake"
	CrashAfterHandshake  = "crash-after-handshake"
	DelayHandshake       = "delay-handshake"
	IncompatibleVersion  = "incompatible-version"
	MalformedInfo        = "malformed-info"
	FailPreflight        = "fail-preflight"
	FailRun              = "fail-run"
	NotFound             = "not-found"
	ExecEcho             = "exec-echo"
	ExecNonzero          = "exec-nonzero"
	ExecCrash            = "exec-crash"
	ExecSlow             = "exec-slow"
	Logs                 = "logs"
)

// Config controls fixture behavior. StateDir is required and persists across launches.
type Config struct {
	StateDir string
	Mode     string
}

// Driver implements the protocol without launching a real container or command.
// Exec echoes bytes rather than interpreting argv or running a shell.
type Driver struct {
	runtimev1.UnimplementedRuntimeDriverServer
	mu     sync.Mutex
	config Config
}

// New validates configuration and creates a private state directory.
func New(config Config) (*Driver, error) {
	if config.StateDir == "" {
		return nil, errors.New("fake runtime requires a state directory")
	}
	if config.Mode == "" {
		config.Mode = Normal
	}
	modes := []string{
		Normal, CrashBeforeHandshake, CrashAfterHandshake, DelayHandshake,
		IncompatibleVersion, MalformedInfo, FailPreflight, FailRun, NotFound,
		ExecEcho, ExecNonzero, ExecCrash, ExecSlow, Logs,
	}
	if !slices.Contains(modes, config.Mode) {
		return nil, errors.New("unknown fake runtime mode")
	}
	if err := os.MkdirAll(config.StateDir, 0o700); err != nil {
		return nil, err
	}
	return &Driver{config: config}, nil
}

// Info reports supported capabilities or the selected discovery failure.
func (d *Driver) Info(context.Context, *runtimev1.InfoRequest) (*runtimev1.InfoResponse, error) {
	if d.config.Mode == CrashAfterHandshake {
		os.Exit(23)
	}
	info := &runtimev1.InfoResponse{
		ApiMajor: runtimev1.APIMajor, ApiMinor: runtimev1.APIMinor,
		DriverName: "fake-runtime", DriverVersion: "1.0.0", RuntimeName: "fake",
		Capabilities: &runtimev1.Capabilities{
			MountTypes: []runtimev1.MountType{
				runtimev1.MountType_MOUNT_TYPE_BIND,
				runtimev1.MountType_MOUNT_TYPE_VOLUME, runtimev1.MountType_MOUNT_TYPE_TMPFS,
			},
			RecreateMode:          runtimev1.RecreateMode_RECREATE_MODE_STOP,
			ProvisioningPreflight: true, Logs: true,
		},
	}
	if d.config.Mode == IncompatibleVersion {
		info.ApiMajor++
	}
	if d.config.Mode == MalformedInfo {
		info.DriverName = ""
	}
	return info, nil
}

// Preflight simulates dependency validation.
func (d *Driver) Preflight(
	ctx context.Context,
	_ *runtimev1.PreflightRequest,
) (*runtimev1.PreflightResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if d.config.Mode == FailPreflight {
		return nil, runtimeError(
			codes.Unavailable,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE,
			"fake preflight failed",
			true,
		)
	}
	return &runtimev1.PreflightResponse{}, nil
}

// ProvisioningPreflight uses the same controllable failure as Preflight.
func (d *Driver) ProvisioningPreflight(
	ctx context.Context,
	_ *runtimev1.ProvisioningPreflightRequest,
) (*runtimev1.ProvisioningPreflightResponse, error) {
	if _, err := d.Preflight(ctx, &runtimev1.PreflightRequest{}); err != nil {
		return nil, err
	}
	return &runtimev1.ProvisioningPreflightResponse{}, nil
}

// TargetArchitecture returns a canonical architecture independent of the test host.
func (*Driver) TargetArchitecture(
	context.Context,
	*runtimev1.TargetArchitectureRequest,
) (*runtimev1.TargetArchitectureResponse, error) {
	return &runtimev1.TargetArchitectureResponse{Architecture: "amd64"}, nil
}

func runtimeError(
	code codes.Code,
	category runtimev1.RuntimeErrorCode,
	message string,
	retryable bool,
) error {
	st, err := status.New(code, message).WithDetails(&runtimev1.RuntimeError{
		Code: category, Message: message, Retryable: retryable,
	})
	if err != nil {
		return status.Error(codes.Internal, "could not encode runtime error")
	}
	return st.Err()
}
