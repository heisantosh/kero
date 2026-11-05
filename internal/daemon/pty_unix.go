//go:build darwin || linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// ptyProc is a program running in a pseudo-terminal of its own.
type ptyProc struct {
	master  *os.File
	cmd     *exec.Cmd
	pid     int
	wakeR   *os.File
	wakeW   *os.File
	mu      sync.Mutex
	stopped bool
	exited  chan struct{}
	code    int
}

type ptyOptions struct {
	Path       string
	Args       []string
	Dir        string
	Env        []string
	Cols, Rows int
	// InheritCursor is for ConPTY, whose console has a cursor of its own;
	// a Unix program draws from the terminal's.
	InheritCursor bool
}

func startPTY(o ptyOptions) (*ptyProc, error) {
	master, slaveName, err := openMaster()
	if err != nil {
		return nil, err
	}
	slave, err := openSlave(slaveName)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: opening %s: %w", slaveName, err)
	}
	defer slave.Close() // the child has its own copies
	if err := setSize(master, o.Cols, o.Rows, 0, 0); err != nil {
		master.Close()
		return nil, err
	}
	cmd := &exec.Cmd{Path: o.Path, Args: o.Args, Dir: o.Dir, Env: o.Env, Stdin: slave, Stdout: slave, Stderr: slave}
	// A session of its own, whose controlling terminal is the slave, so
	// that the terminal's signals reach the process group in the
	// foreground, and nothing ties it to the daemon's.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return newPTYProc(master, cmd)
}

func (p *ptyProc) Write(b []byte) (int, error) { return p.master.Write(b) }

// Resize resizes the terminal, a cell being cellW by cellH pixels (0 when
// unknown).
func (p *ptyProc) Resize(cols, rows, cellW, cellH int) error {
	return setSize(p.master, cols, rows, cellW, cellH)
}

// ClearScreen does nothing: a Unix terminal keeps no screen of its own,
// and shells move the cursor relative to where it is.
func (p *ptyProc) ClearScreen() {}

// Close hangs up the terminal: its processes get SIGHUP.
func (p *ptyProc) Close() error {
	p.stopRead()
	p.hangup()
	err := p.master.Close()
	p.closeWakes()
	return err
}

// Foreground returns the process group in the terminal's foreground.
func (p *ptyProc) Foreground() int {
	var pgid int32
	if ioctl(p.master, syscall.TIOCGPGRP, unsafe.Pointer(&pgid)) != nil {
		return 0
	}
	return int(pgid)
}

func setSize(f *os.File, cols, rows, cellW, cellH int) error {
	ws := struct{ row, col, xpixel, ypixel uint16 }{clamp(rows), clamp(cols), pixels(cols, cellW), pixels(rows, cellH)}
	return ioctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
}

func clamp(v int) uint16 { return uint16(max(1, min(v, 0xFFFF))) }

// pixels is the extent of n cells of size cell, 0 when unknown.
func pixels(n, cell int) uint16 { return uint16(max(0, min(n*cell, 0xFFFF))) }

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	err = c.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	})
	if err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
