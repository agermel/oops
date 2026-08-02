//go:build windows

package workspace

import (
	"os"
	"os/exec"
	"strconv"
)

func configureShellProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
