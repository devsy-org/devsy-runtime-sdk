//go:build linux || darwin

package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type processTree struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	observeExit func() error
	reaped      bool
}

func configurePipes(cmd *exec.Cmd, lease, config *os.File) error {
	cmd.ExtraFiles = []*os.File{lease, config}
	cmd.Args = append(cmd.Args, "--lease=3", "--config=4")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func preventLeaseInheritance(file *os.File) { unix.CloseOnExec(int(file.Fd())) }

func startTree(cfg configuration) (*processTree, error) {
	if err := adoptDescendants(); err != nil {
		return nil, err
	}
	cmd := runtimeCommand(cfg)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	watch, err := watchExit(cmd.Process.Pid)
	if err != nil {
		_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		_ = cmd.Wait()
		return nil, err
	}
	return &processTree{cmd: cmd, observeExit: watch}, nil
}

func (t *processTree) Wait() error {
	observed := t.observeExit()
	t.mu.Lock()
	defer t.mu.Unlock()
	// Observe exit without reaping: the leader PID remains reserved until group termination.
	killed := t.killGroup()
	waited := t.cmd.Wait()
	t.reaped = true
	return errors.Join(observed, killed, waited, reapDescendants(t.cmd.Process.Pid))
}

func (t *processTree) Kill() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reaped {
		return nil
	}
	return t.killGroup()
}

func (t *processTree) killGroup() error {
	return terminateGroup(t.cmd.Process.Pid)
}
