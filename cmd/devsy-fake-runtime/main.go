// Command devsy-fake-runtime serves the SDK's persistent protocol test fixture.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
)

func main() {
	config := fake.Config{}
	flag.StringVar(
		&config.StateDir,
		"state-dir",
		"",
		"private state directory (required; one active plugin owner)",
	)
	flag.StringVar(&config.Mode, "mode", fake.Normal, "fixture scenario; see SDK README")
	delay := flag.Duration(
		"handshake-delay",
		time.Minute,
		"delay used only by delay-handshake mode",
	)
	mounts := flag.String("mount-types", "all", "all, none, or comma-separated bind,volume,tmpfs")
	flag.Parse()
	types, err := parseMountTypes(*mounts)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	config.MountTypes = types
	driver, err := fake.New(config)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if config.Mode == fake.CrashBeforeHandshake {
		os.Exit(22)
	}
	if config.Mode == fake.DelayHandshake {
		time.Sleep(*delay)
	}
	server.Serve(driver)
}

func parseMountTypes(value string) ([]runtimev1.MountType, error) {
	if value == "all" {
		return nil, nil
	}
	result := make([]runtimev1.MountType, 0)
	if value == "none" {
		return result, nil
	}
	types := map[string]runtimev1.MountType{
		"bind":   runtimev1.MountType_MOUNT_TYPE_BIND,
		"volume": runtimev1.MountType_MOUNT_TYPE_VOLUME,
		"tmpfs":  runtimev1.MountType_MOUNT_TYPE_TMPFS,
	}
	for name := range strings.SplitSeq(value, ",") {
		mount, ok := types[name]
		if !ok {
			return nil, errors.New("unknown mount type")
		}
		result = append(result, mount)
	}
	return result, nil
}
