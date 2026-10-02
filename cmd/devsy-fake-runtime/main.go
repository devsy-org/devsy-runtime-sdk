// Command devsy-fake-runtime serves the SDK's persistent protocol test fixture.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
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
	flag.Parse()
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
