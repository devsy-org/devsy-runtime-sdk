package fake_test

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance"
	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/protobuf/proto"
)

func TestMountCapabilityProfiles(t *testing.T) {
	for _, profile := range []string{allMounts, "none", "bind", "volume", "tmpfs"} {
		t.Run(profile, func(t *testing.T) {
			driver := connect(t, launchProcess(t, launchOptions{
				directory: t.TempDir(),
				mode:      fake.Normal,
				mounts:    profile,
				timeout:   5 * time.Second,
			}))
			info, err := driver.Info(testContext(t), &runtimev1.InfoRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if err := runtimev1.ValidateInfo(info); err != nil {
				t.Fatal(err)
			}
			assertMountProfile(t, driver, info, profile)
		})
	}
}

func assertMountProfile(
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
	info *runtimev1.InfoResponse,
	profile string,
) {
	t.Helper()
	mounts := map[string]runtimev1.MountType{
		"bind":   runtimev1.MountType_MOUNT_TYPE_BIND,
		"volume": runtimev1.MountType_MOUNT_TYPE_VOLUME,
		"tmpfs":  runtimev1.MountType_MOUNT_TYPE_TMPFS,
	}
	var wantCount int
	for name, mount := range mounts {
		supported := profile == allMounts || profile == name
		if supported {
			wantCount++
		}
		request := &runtimev1.RunImageRequest{
			WorkspaceId: name,
			Image:       testImage,
			Mounts:      []*runtimev1.Mount{{Type: mount, Target: "/mount"}},
		}
		_, err := driver.RunImage(testContext(t), request)
		assertMountResult(t, err, supported)
		if supported {
			continue
		}
		// A rejected create must not persist a resource.
		found, err := driver.Find(testContext(t), &runtimev1.FindRequest{WorkspaceId: name})
		if err != nil || found.GetFound() {
			t.Fatalf("unsupported create left state: %v, %v", found, err)
		}
	}
	if len(info.GetCapabilities().GetMountTypes()) != wantCount {
		t.Fatal("advertised mount count differs")
	}
}

func assertMountResult(t *testing.T, err error, supported bool) {
	t.Helper()
	if supported {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	conformance.RequireRuntimeError(
		t,
		err,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED,
	)
}

func TestInfoDoesNotShareMutableCapabilities(t *testing.T) {
	mounts := []runtimev1.MountType{runtimev1.MountType_MOUNT_TYPE_BIND}
	driver, err := fake.New(fake.Config{StateDir: t.TempDir(), MountTypes: mounts})
	if err != nil {
		t.Fatal(err)
	}
	mounts[0] = runtimev1.MountType_MOUNT_TYPE_VOLUME
	before, err := driver.Info(testContext(t), &runtimev1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	clone := proto.CloneOf(before)
	before.Capabilities.MountTypes[0] = runtimev1.MountType_MOUNT_TYPE_TMPFS
	after, err := driver.Info(testContext(t), &runtimev1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(clone, after) ||
		after.GetCapabilities().GetMountTypes()[0] != runtimev1.MountType_MOUNT_TYPE_BIND {
		t.Fatal("Info or config aliases mutable capability storage")
	}
}
