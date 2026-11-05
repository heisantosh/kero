//go:build illumos || solaris

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// STREAMS and ptm/pts ioctls on illumos/Solaris (sys/ptms.h, sys/stropts.h).
const (
	isptm   = 0x5001
	unlkpt  = 0x5002
	ownerpt = 0x5005
	iPush   = 0x5302
	iFind   = 0x530b
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
	Path          string
	Args          []string
	Dir           string
	Env           []string
	Cols, Rows    int
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return newPTYProc(master, cmd)
}

func (p *ptyProc) Write(b []byte) (int, error) { return p.master.Write(b) }

func (p *ptyProc) Resize(cols, rows, cellW, cellH int) error {
	return setSize(p.master, cols, rows, cellW, cellH)
}

func (p *ptyProc) ClearScreen() {}

func (p *ptyProc) Close() error {
	p.stopRead()
	p.hangup()
	err := p.master.Close()
	p.closeWakes()
	return err
}

func (p *ptyProc) Foreground() int {
	select {
	case <-p.exited:
		return 0
	default:
	}
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

func pixels(n, cell int) uint16 { return uint16(max(0, min(n*cell, 0xFFFF))) }

func openMaster() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("pty: %w", err)
	}
	c, err := master.SyscallConn()
	if err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty: %w", err)
	}
	var minor uint32
	var ioctlErr error
	if err := c.Control(func(fd uintptr) {
		if _, ioctlErr = unix.IoctlSetStrioctlRetInt(int(fd), unix.I_STR, &unix.Strioctl{Cmd: isptm}); ioctlErr != nil {
			return
		}
		own := [2]uint32{uint32(os.Getuid()), uint32(os.Getgid())}
		if _, ioctlErr = unix.IoctlSetStrioctlRetInt(int(fd), unix.I_STR, &unix.Strioctl{
			Cmd: ownerpt,
			Len: int32(unsafe.Sizeof(own)),
			Dp:  (*int8)(unsafe.Pointer(&own[0])),
		}); ioctlErr != nil {
			return
		}
		if _, ioctlErr = unix.IoctlSetStrioctlRetInt(int(fd), unix.I_STR, &unix.Strioctl{Cmd: unlkpt}); ioctlErr != nil {
			return
		}
		var st unix.Stat_t
		if ioctlErr = unix.Fstat(int(fd), &st); ioctlErr != nil {
			return
		}
		minor = unix.Minor(st.Rdev)
	}); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty: %w", err)
	}
	if ioctlErr != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty: %w", ioctlErr)
	}
	return master, "/dev/pts/" + strconv.FormatUint(uint64(minor), 10), nil
}

func openSlave(name string) (*os.File, error) {
	slave, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	c, err := slave.SyscallConn()
	if err != nil {
		slave.Close()
		return nil, err
	}
	var ioctlErr error
	if err := c.Control(func(fd uintptr) {
		for _, mod := range []string{"ptem", "ldterm", "ttcompat"} {
			b, _ := unix.ByteSliceFromString(mod)
			r, err := unix.IoctlSetIntRetInt(int(fd), iFind, int(uintptr(unsafe.Pointer(&b[0]))))
			if err == nil && r == 0 {
				if ioctlErr = unix.IoctlSetString(int(fd), iPush, mod); ioctlErr != nil {
					return
				}
			}
		}
	}); err != nil {
		slave.Close()
		return nil, err
	}
	if ioctlErr != nil {
		slave.Close()
		return nil, ioctlErr
	}
	return slave, nil
}

func (p *ptyProc) hangup() {}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := c.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetTermio(int(fd), int(req), (*unix.Termio)(arg))
		if ioctlErr == syscall.ENOTTY && req == syscall.TIOCGPGRP {
			var st unix.Stat_t
			if err := unix.Fstat(int(fd), &st); err != nil {
				ioctlErr = err
				return
			}
			pts := "/dev/pts/" + strconv.FormatUint(uint64(unix.Minor(st.Rdev)), 10)
			sfd, err := unix.Open(pts, unix.O_RDONLY|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
			if err != nil {
				ioctlErr = err
				return
			}
			ioctlErr = unix.IoctlSetTermio(sfd, int(req), (*unix.Termio)(arg))
			unix.Close(sfd)
		}
	}); err != nil {
		return err
	}
	return ioctlErr
}
