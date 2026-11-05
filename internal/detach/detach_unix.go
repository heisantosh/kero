//go:build !windows

// Package detach starts programs that outlive whoever started them, as
// kerod, which the app, the CLI and a bridge start, and which starts the
// kerod that follows it as it restarts.
package detach

import (
	"os/exec"
	"runtime"
	"syscall"
)

// Start starts cmd, returning what runs it, in a session of its own, so
// that it outlives whoever started it.
func Start(cmd *exec.Cmd) (*exec.Cmd, error) {
	attr := &syscall.SysProcAttr{Setsid: true}
	if runtime.GOOS == "illumos" {
		// On illumos TIOCSCTTY fails with ENOTTY when the process that
		// starts a pane's shell is a session leader. A process group of
		// its own keeps kerod out of the terminal's foreground group,
		// and it catches SIGHUP, so it outlives the session all the same.
		attr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.SysProcAttr = attr
	return cmd, cmd.Start()
}
