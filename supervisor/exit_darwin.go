package supervisor

import (
	"errors"

	"golang.org/x/sys/unix"
)

func adoptDescendants() error { return nil }
func reapDescendants() error  { return nil }

func watchExit(pid int) (func() error, error) {
	queue, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(queue)
	event := unix.Kevent_t{Fflags: unix.NOTE_EXIT}
	unix.SetKevent(&event, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	_, err = unix.Kevent(queue, []unix.Kevent_t{event}, nil, nil)
	if err != nil {
		_ = unix.Close(queue)
		// An already-exited child remains our unreaped zombie; its PID is still reserved.
		if errors.Is(err, unix.ESRCH) {
			return func() error { return nil }, nil
		}
		return nil, err
	}
	return func() error {
		defer func() { _ = unix.Close(queue) }()
		var events [1]unix.Kevent_t
		for {
			_, err := unix.Kevent(queue, nil, events[:], nil)
			if !errors.Is(err, unix.EINTR) {
				return err
			}
		}
	}, nil
}

func terminateGroup(pid int) error {
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if !errors.Is(err, unix.EPERM) {
		return err
	}
	// Darwin reports EPERM for a zombie-only group. Preserve real permission failures.
	processes, queryErr := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
	if queryErr != nil {
		return errors.Join(err, queryErr)
	}
	const zombieState = 5 // SZOMB in Darwin's proc.h.
	for _, process := range processes {
		if process.Proc.P_stat != zombieState {
			return err
		}
	}
	return nil
}
