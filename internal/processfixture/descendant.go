package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
)

func superviseFixture(observer string, args []string) {
	// #nosec G704 -- Address is supplied by the test-owned loopback observer, not external input.
	conn, err := net.Dial("tcp", observer)
	if err != nil {
		panic(err)
	}
	events := &reporter{encoder: json.NewEncoder(conn), role: "supervisor", address: observer}
	if err := events.Encode(
		event{Role: "supervisor", Kind: "ready", PID: os.Getpid()},
	); err != nil {
		panic(err)
	}
	disconnected := make(chan struct{})
	go events.respond(conn, disconnected)
	supervisor.Main(args)
}

func launchGrandchild(events *reporter) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// #nosec G204 -- Fixed self-executable fixture, never a user command.
	cmd := exec.Command(
		executable,
		"--mode",
		"grandchild",
		"--observer",
		events.address,
		"--ignore-interrupt=true",
	)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = output.Close()
		return err
	}
	var ready event
	if err := json.NewDecoder(output).Decode(&ready); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if ready.PID != cmd.Process.Pid {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New("grandchild readiness mismatch")
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func maybeGrandchild(events *reporter, enabled bool) error {
	if !enabled {
		return nil
	}
	return launchGrandchild(events)
}
