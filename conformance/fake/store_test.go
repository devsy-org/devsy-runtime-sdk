package fake_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
)

func TestConfigValidation(t *testing.T) {
	for _, config := range []fake.Config{{}, {StateDir: t.TempDir(), Mode: "unknown"}} {
		if _, err := fake.New(config); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestCorruptStateIsAnError(t *testing.T) {
	for _, data := range []string{"not JSON", `{}`} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			driver, err := fake.New(fake.Config{StateDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			_, err = driver.RunImage(
				context.Background(),
				&runtimev1.RunImageRequest{WorkspaceId: workspaceID, Image: testImage},
			)
			if err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("state files: %v, %v", files, err)
			}
			if err := os.WriteFile(files[0], []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = driver.Find(
				context.Background(),
				&runtimev1.FindRequest{WorkspaceId: workspaceID},
			)
			assertRuntimeError(
				t,
				err,
				codes.Internal,
				runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE,
			)
			_, err = driver.Start(
				context.Background(),
				&runtimev1.StartRequest{WorkspaceId: workspaceID},
			)
			assertRuntimeError(
				t,
				err,
				codes.Internal,
				runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE,
			)
		})
	}
}
