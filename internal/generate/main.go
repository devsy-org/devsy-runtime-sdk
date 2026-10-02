// Command generate builds pinned protobuf tools and regenerates Runtime Protocol bindings.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	tools, err := os.MkdirTemp("", "devsy-proto-tools-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(tools); err != nil {
			log.Print(err)
		}
	}()
	for _, module := range []string{
		"google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12",
		"google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2",
	} {
		cmd := exec.Command(
			"go",
			"install",
			module,
		) // #nosec G204 -- Module paths and versions are fixed above.
		cmd.Env = append(os.Environ(), "GOBIN="+tools)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("install %s: %w", module, err)
		}
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	// #nosec G204 -- Arguments use fixed schema paths and a locally created temporary tool directory.
	cmd := exec.Command(
		"protoc",
		"-I",
		"proto",
		"--plugin=protoc-gen-go="+filepath.Join(tools, "protoc-gen-go"+suffix),
		"--plugin=protoc-gen-go-grpc="+filepath.Join(tools, "protoc-gen-go-grpc"+suffix),
		"--go_out=.",
		"--go_opt=module=github.com/devsy-org/devsy-runtime-sdk",
		"--go-grpc_out=.",
		"--go-grpc_opt=module=github.com/devsy-org/devsy-runtime-sdk",
		"proto/devsy/runtime/v1/runtime.proto",
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
