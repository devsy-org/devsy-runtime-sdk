package runtimev1

import "fmt"

const (
	// APIMajor changes when the runtime wire contract breaks compatibility.
	APIMajor uint32 = 1
	// APIMinor tracks compatible additions within the current major generation.
	APIMinor uint32 = 0
	// ChunkSize is the recommended maximum payload for each Exec data frame.
	ChunkSize = 32 * 1024
)

// ValidateInfo checks the behavior the host needs before invoking a runtime.
// Same-major newer minor versions are compatible; unknown behavioral enum
// values are rejected rather than silently selecting an unsafe policy.
func ValidateInfo(info *InfoResponse) error {
	if info == nil {
		return fmt.Errorf("runtime Info response is missing")
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
		return fmt.Errorf("runtime Info requires driver name, driver version, and runtime name")
	}
	return validateCapabilities(info.DriverName, info.Capabilities)
}

func validateCapabilities(driverName string, caps *Capabilities) error {
	if caps == nil {
		return fmt.Errorf("runtime Info capabilities are missing")
	}
	if caps.RecreateMode != RecreateMode_RECREATE_MODE_DELETE &&
		caps.RecreateMode != RecreateMode_RECREATE_MODE_STOP {
		return fmt.Errorf(
			"runtime %q has unsupported recreate mode %d",
			driverName,
			caps.RecreateMode,
		)
	}
	for _, mount := range caps.MountTypes {
		if mount != MountType_MOUNT_TYPE_BIND && mount != MountType_MOUNT_TYPE_VOLUME &&
			mount != MountType_MOUNT_TYPE_TMPFS {
			return fmt.Errorf("runtime %q has unsupported mount type %d", driverName, mount)
		}
	}
	return nil
}
