// Package runtimev1 provides Runtime Protocol v1 messages, RPC bindings, and compatibility validation.
package runtimev1

import (
	"errors"
	"fmt"
)

const (
	// APIMajor changes when the runtime wire contract breaks compatibility.
	APIMajor uint32 = 1
	// APIMinor tracks compatible additions within the current major generation.
	APIMinor uint32 = 3
	// ChunkSize is the recommended maximum payload for each Exec data frame.
	ChunkSize = 32 * 1024
)

// ValidateInfo checks the behavior the host needs before invoking a runtime.
// Same-major newer minor versions are compatible; unknown behavioral enum
// values are rejected rather than silently selecting an unsafe policy.
func ValidateInfo(info *InfoResponse) error {
	if info == nil {
		return errors.New("runtime Info response is missing")
	}
	if info.ApiMajor != APIMajor {
		return fmt.Errorf(
			"runtime %q (%s) API %d.%d is incompatible with host API %d.%d; install a runtime supporting API major %d",
			info.DriverName,
			info.DriverVersion,
			info.ApiMajor,
			info.ApiMinor,
			APIMajor,
			APIMinor,
			APIMajor,
		)
	}
	if info.DriverName == "" || info.DriverVersion == "" || info.RuntimeName == "" {
		return errors.New("runtime Info requires driver name, driver version, and runtime name")
	}
	return validateCapabilities(info.DriverName, info.ApiMinor, info.Capabilities)
}

func validateCapabilities(driverName string, apiMinor uint32, caps *Capabilities) error {
	if caps == nil {
		return errors.New("runtime Info capabilities are missing")
	}
	if caps.ReusePreflight && apiMinor < 2 {
		return errors.New("reuse preflight requires API 1.2 or newer")
	}
	if err := validateRecreateMode(driverName, apiMinor, caps.RecreateMode); err != nil {
		return err
	}
	for _, mount := range caps.MountTypes {
		switch mount {
		case MountType_MOUNT_TYPE_BIND, MountType_MOUNT_TYPE_VOLUME, MountType_MOUNT_TYPE_TMPFS:
		default:
			return fmt.Errorf("runtime %q has unsupported mount type %d", driverName, mount)
		}
	}
	return nil
}

func validateRecreateMode(driverName string, apiMinor uint32, mode RecreateMode) error {
	switch mode {
	case RecreateMode_RECREATE_MODE_DELETE, RecreateMode_RECREATE_MODE_STOP:
		return nil
	case RecreateMode_RECREATE_MODE_ON_RUN:
		if apiMinor < 3 {
			return errors.New("recreate on run requires API 1.3 or newer")
		}
		return nil
	default:
		return fmt.Errorf("runtime %q has unsupported recreate mode %d", driverName, mode)
	}
}
