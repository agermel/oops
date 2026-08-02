//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package workspace

import "os/exec"

func configureShellProcess(_ *exec.Cmd) {}
