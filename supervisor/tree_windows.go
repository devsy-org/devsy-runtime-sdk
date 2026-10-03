package supervisor

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct {
	cmd *exec.Cmd
	job windows.Handle
}

func configurePipes(cmd *exec.Cmd, lease, config *os.File) error {
	handles := []syscall.Handle{syscall.Handle(lease.Fd()), syscall.Handle(config.Fd())}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(
			windows.Handle(handle),
			windows.HANDLE_FLAG_INHERIT,
			windows.HANDLE_FLAG_INHERIT,
		); err != nil {
			return err
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: handles}
	cmd.Args = append(
		cmd.Args,
		"--lease="+strconv.FormatUint(uint64(lease.Fd()), 10),
		"--config="+strconv.FormatUint(uint64(config.Fd()), 10),
	)
	return nil
}

func preventLeaseInheritance(file *os.File) {
	// exec.Cmd also supplies an explicit handle list; neither lease nor job reaches the plugin.
	_ = windows.SetHandleInformation(windows.Handle(file.Fd()), windows.HANDLE_FLAG_INHERIT, 0)
}

func startTree(cfg configuration) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	var pinned runtime.Pinner
	pinned.Pin(&limits)
	defer pinned.Unpin()
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		// #nosec G103 -- Pinned, fixed-size Win32 structure; the native API requires this pointer.
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	// Join before creating the plugin: CreateProcess descendants inherit membership atomically.
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	// This handle is intentionally held until Main exits. Closing it here would kill this supervisor too.
	cmd := runtimeCommand(cfg)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{cmd: cmd, job: job}, nil
}

func (t *processTree) Wait() error { return t.cmd.Wait() }
func (t *processTree) Kill() error { return windows.TerminateJobObject(t.job, 0) }
