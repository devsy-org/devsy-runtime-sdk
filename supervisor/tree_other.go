//go:build !linux && !darwin && !windows

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

type processTree struct{}

func unsupported() error {
	return fmt.Errorf("runtime process supervision is unsupported on %s", runtime.GOOS)
}
func configurePipes(_ *exec.Cmd, _, _ *os.File) error { return unsupported() }
func preventLeaseInheritance(_ *os.File)              {}
func startTree(configuration) (*processTree, error)   { return nil, unsupported() }
func (*processTree) Wait() error                      { return unsupported() }
func (*processTree) Kill() error                      { return unsupported() }
