package fake_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestRecreateDiscoveryDoesNotGrantPermission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		driver, err := fake.New(fake.Config{StateDir: t.TempDir(), RecreateOnRun: enabled})
		if err != nil {
			t.Fatal(err)
		}
		assertRecreateProfileNegotiation(t, driver, enabled)
		request := &runtimev1.RunImageRequest{
			WorkspaceId: workspaceID, Image: testImage, AllowRecreate: enabled,
		}
		if _, err := driver.RunImage(testContext(t), request); err != nil {
			t.Fatal(err)
		}
		// Even prior opt-in does not authorize false. Default STOP also rejects true.
		request.AllowRecreate = !enabled
		_, err = driver.RunImage(testContext(t), request)
		assertRuntimeError(t, err, codes.AlreadyExists,
			runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS)
	}
}

func assertRecreateProfileNegotiation(t *testing.T, driver *fake.Driver, enabled bool) {
	t.Helper()
	for _, request := range []*runtimev1.InfoRequest{
		nil, {}, {SupportsRecreateOnRun: true}, {}, {SupportsRecreateOnRun: true},
	} {
		info, err := driver.Info(testContext(t), request)
		if err != nil {
			t.Fatal(err)
		}
		want := runtimev1.RecreateMode_RECREATE_MODE_STOP
		if enabled {
			want = runtimev1.RecreateMode_RECREATE_MODE_DELETE
			if request.GetSupportsRecreateOnRun() {
				want = runtimev1.RecreateMode_RECREATE_MODE_ON_RUN
			}
		}
		if info.GetCapabilities().GetRecreateMode() != want {
			t.Fatalf("enabled=%v request=%v: got %v, want %v", enabled, request, info, want)
		}
		if err := runtimev1.ValidateInfo(info); err != nil {
			t.Fatal(err)
		}
		info.Capabilities.RecreateMode = runtimev1.RecreateMode_RECREATE_MODE_UNSPECIFIED
		info.Capabilities.MountTypes[0] = runtimev1.MountType_MOUNT_TYPE_UNSPECIFIED
	}
}

func launchRecreate(t *testing.T, directory string) *hplugin.Client {
	t.Helper()
	return launchProcess(t, launchOptions{
		directory: directory, mode: fake.Normal, timeout: 5 * time.Second,
		mounts: allMounts, recreateOnRun: true,
	})
}

func TestRecreateAuthorizationAcrossProcesses(t *testing.T) {
	for _, state := range []string{"running", "stopped"} {
		t.Run(state, func(t *testing.T) {
			directory := t.TempDir()
			process := launchRecreate(t, directory)
			driver := connect(t, process)
			createWorkspace(t, driver)
			if state == "stopped" {
				if _, err := driver.Stop(testContext(t), &runtimev1.StopRequest{
					WorkspaceId: workspaceID,
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertRecreateRPCNegotiation(t, driver)
			assertRejectedRecreationPreservesState(t, driver, directory)
			process.Kill()
			if !process.Exited() {
				t.Fatal("initial process was not reaped")
			}
			assertFreshProcessRecreation(t, directory)
		})
	}
}

func assertRecreateRPCNegotiation(t *testing.T, driver runtimev1.RuntimeDriverClient) {
	t.Helper()
	info, err := driver.Info(
		testContext(t),
		&runtimev1.InfoRequest{SupportsRecreateOnRun: true},
	)
	mode := info.GetCapabilities().GetRecreateMode()
	if err != nil || mode != runtimev1.RecreateMode_RECREATE_MODE_ON_RUN {
		t.Fatalf("opt-in discovery: %v, %v", info, err)
	}
	legacy, err := driver.Info(testContext(t), &runtimev1.InfoRequest{})
	mode = legacy.GetCapabilities().GetRecreateMode()
	if err != nil || mode != runtimev1.RecreateMode_RECREATE_MODE_DELETE {
		t.Fatalf("legacy discovery after opt-in: %v, %v", legacy, err)
	}
}

func assertFreshProcessRecreation(t *testing.T, directory string) {
	t.Helper()
	process := launchRecreate(t, directory)
	driver := connect(t, process)
	// No Info call in this process: authorization belongs to each RunImage.
	assertRejectedRecreationPreservesState(t, driver, directory)
	request := &runtimev1.RunImageRequest{
		WorkspaceId: workspaceID, Image: testImage, User: "2000",
		Labels: []string{"generation=replaced"}, AllowRecreate: true,
	}
	if _, err := driver.RunImage(testContext(t), request); err != nil {
		t.Fatal(err)
	}
	after := findRecreateWorkspace(t, driver)
	if after.GetContainer().GetConfig().GetLabels()["generation"] != "replaced" ||
		after.GetContainer().GetConfig().GetUser() != "2000" ||
		after.GetContainer().GetState().GetStatus() != "running" {
		t.Fatalf("replacement did not apply new configuration: %v", after)
	}
	process.Kill()
	if !process.Exited() {
		t.Fatal("replacement process was not reaped")
	}
	driver = connect(t, launchRecreate(t, directory))
	if !proto.Equal(after, findRecreateWorkspace(t, driver)) {
		t.Fatal("replacement did not persist across process restart")
	}
	assertRejectedRecreationPreservesState(t, driver, directory)
}

func assertRejectedRecreationPreservesState(
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
	directory string,
) {
	t.Helper()
	before := findRecreateWorkspace(t, driver)
	disk := snapshotRecreateDirectory(t, directory)
	for _, test := range []struct {
		request  *runtimev1.RunImageRequest
		code     codes.Code
		category runtimev1.RuntimeErrorCode
	}{
		{
			&runtimev1.RunImageRequest{WorkspaceId: workspaceID, Image: testImage},
			codes.AlreadyExists, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS,
		},
		{
			&runtimev1.RunImageRequest{WorkspaceId: workspaceID, AllowRecreate: true},
			codes.InvalidArgument, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT,
		},
		{
			&runtimev1.RunImageRequest{
				WorkspaceId: workspaceID, Image: testImage, AllowRecreate: true,
				WorkspaceMount: &runtimev1.Mount{Type: runtimev1.MountType(99)},
			},
			codes.Unimplemented, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED,
		},
	} {
		_, err := driver.RunImage(testContext(t), test.request)
		assertRuntimeError(t, err, test.code, test.category)
		if !proto.Equal(before, findRecreateWorkspace(t, driver)) {
			t.Fatal("rejected request changed complete Find result")
		}
		after := snapshotRecreateDirectory(t, directory)
		if len(disk) != len(after) {
			t.Fatal("rejected request changed persisted file count")
		}
		for name, data := range disk {
			if !bytes.Equal(data, after[name]) {
				t.Fatalf("rejected request changed persisted file %q", name)
			}
		}
	}
}

func findRecreateWorkspace(
	t *testing.T,
	driver runtimev1.RuntimeDriverClient,
) *runtimev1.FindResponse {
	t.Helper()
	found, err := driver.Find(testContext(t), &runtimev1.FindRequest{WorkspaceId: workspaceID})
	if err != nil || !found.GetFound() {
		t.Fatalf("find recreation workspace: %v, %v", found, err)
	}
	return found
}

func snapshotRecreateDirectory(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		// #nosec G304 -- Directory and files belong to this private fixture instance.
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = data
	}
	return result
}
