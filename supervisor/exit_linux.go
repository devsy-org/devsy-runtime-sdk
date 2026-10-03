package supervisor

import (
	"errors"

	"golang.org/x/sys/unix"
)

func adoptDescendants() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

func watchExit(pid int) (func() error, error) {
	return func() error {
		for {
			var info unix.Siginfo
			err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if !errors.Is(err, unix.EINTR) {
				return err
			}
		}
	}, nil
}

func reapDescendants() error {
	for {
		var status unix.WaitStatus
		_, err := unix.Wait4(-1, &status, 0, nil)
		if errors.Is(err, unix.ECHILD) {
			return nil
		}
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func terminateGroup(pid int) error {
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
