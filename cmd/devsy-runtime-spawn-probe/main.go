// Command devsy-runtime-spawn-probe measures trusted runtime plugin startup and shutdown.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/internal/spawnprobe"
)

func main() {
	config := spawnprobe.Config{Diagnostics: os.Stderr}
	flag.StringVar(
		&config.Binary,
		"binary",
		"",
		"absolute path to an explicitly trusted plugin executable",
	)
	flag.IntVar(&config.Samples, "samples", 100, "warm repetitions per operation")
	flag.DurationVar(
		&config.Timeout,
		"timeout",
		15*time.Second,
		"startup/RPC timeout per repetition; reaping is measured separately",
	)
	flag.StringVar(
		&config.WorkspaceID,
		"workspace-id",
		"spawn-probe-absent",
		"unused workspace ID for Find",
	)
	flag.StringVar(&config.Revision, "revision", "", "source revision recorded in the report")
	flag.Parse()
	config.Args = flag.Args()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	report, err := spawnprobe.Run(ctx, config)
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fail(err)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
