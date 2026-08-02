//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureShellProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		} else if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
}
