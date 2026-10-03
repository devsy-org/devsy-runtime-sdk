package supervisor

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Main is the dedicated supervisor executable entry point; it always exits the
// process. It must never run inside the host or the runtime plugin process.
// On Windows, exiting closes the supervisor's non-inherited Job Object handle.
func Main(args []string) {
	flags := flag.NewFlagSet("devsy-runtime-supervisor", flag.ContinueOnError)
	leaseFD := flags.Uint64("lease", 0, "inherited host-lifetime pipe")
	configFD := flags.Uint64("config", 0, "inherited configuration pipe")
	if err := flags.Parse(args); err != nil {
		os.Exit(1)
	}
	if *leaseFD == 0 || *configFD == 0 || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "supervisor requires inherited lease and config pipes")
		os.Exit(1)
	}
	lease := os.NewFile(uintptr(*leaseFD), "host-lease")
	config := os.NewFile(uintptr(*configFD), "runtime-config")
	if lease == nil || config == nil {
		fmt.Fprintln(os.Stderr, "invalid supervisor pipe handles")
		os.Exit(1)
	}
	preventLeaseInheritance(lease)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := supervise(ctx, lease, config)
	cancel()
	_ = lease.Close()
	_ = config.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime supervisor:", err)
		os.Exit(1)
	}
	// Do not close a Windows Job Object containing this process before ExitProcess.
	os.Exit(0)
}

func supervise(ctx context.Context, lease, input *os.File) error {
	cfg, err := readConfiguration(input)
	_ = input.Close()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(cfg.SocketDir) }()
	stopped := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, lease); close(stopped) }()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return runTree(ctx, cfg, stopped)
}

func runTree(ctx context.Context, cfg configuration, stopped <-chan struct{}) error {
	tree, err := startTree(cfg)
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- tree.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-stopped:
		return stopTree(cfg, tree, exited)
	case <-ctx.Done():
		return errors.Join(ctx.Err(), stopTree(cfg, tree, exited))
	}
}

func stopTree(cfg configuration, tree *processTree, exited <-chan error) error {
	// Windows termination kills the supervisor itself, so delete its empty socket directory first.
	if err := os.RemoveAll(cfg.SocketDir); err != nil {
		fmt.Fprintln(os.Stderr, "supervisor socket cleanup:", err)
	}
	if err := tree.Kill(); err != nil {
		return err
	}
	<-exited
	return nil
}

func readConfiguration(input *os.File) (configuration, error) {
	var cfg configuration
	decoder := gob.NewDecoder(io.LimitReader(input, 1<<20))
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, errors.New("invalid runtime supervisor configuration")
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func runtimeCommand(cfg configuration) *exec.Cmd {
	// #nosec G204 -- Configuration is supplied by the trusted host over an inherited pipe.
	cmd := exec.Command(cfg.Binary, cfg.Args...)
	cmd.Env, cmd.Dir = cfg.Env, cfg.Directory
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}
