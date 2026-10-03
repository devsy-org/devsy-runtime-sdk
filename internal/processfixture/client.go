package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
)

func (f *fixture) childCommand(ctx context.Context) (*exec.Cmd, error) {
	if f.uncancelable {
		ctx = context.Background()
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- Self-executable fixture, with arguments controlled by the test harness.
	cmd := exec.CommandContext(
		ctx,
		executable,
		"--mode",
		childRole,
		"--observer",
		f.observer,
		fmt.Sprintf(
			"--ignore-interrupt=%t",
			f.ignore,
		),
		fmt.Sprintf("--grandchild=%t", f.grandchild),
	)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	// Windows cannot deliver os.Interrupt to a child; WaitDelay also bounds this fallback.
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func hostClient(observer string, behavior behavior) (*hplugin.Client, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- Self-executable fixture, with arguments controlled by the test harness.
	cmd := exec.Command(
		executable,
		"--mode",
		pluginRole,
		"--observer",
		observer,
		fmt.Sprintf(
			"--ignore-interrupt=%t",
			behavior.ignore,
		),
		fmt.Sprintf("--grandchild=%t", behavior.grandchild),
		fmt.Sprintf("--uncancelable=%t", behavior.uncancelable),
	)
	config := &hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		Cmd:              cmd,
		StartTimeout:     10 * time.Second,
		Logger:           hclog.NewNullLogger(),
		Stderr:           os.Stderr,
		SyncStderr:       os.Stderr,
	}
	if behavior.owned {
		config.Cmd = nil
		config.RunnerFunc = supervisor.Runner(
			supervisor.Options{
				SupervisorBinary: executable,
				SupervisorArgs:   []string{"--supervise", observer},
				RuntimeBinary:    executable,
				Args:             cmd.Args[1:],
			},
		)
	}
	return hplugin.NewClient(config), nil
}
