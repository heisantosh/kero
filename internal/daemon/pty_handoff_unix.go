//go:build darwin || linux || illumos || solaris

package daemon

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// newPTYProc wraps a terminal whose program cmd started, and watches it.
func newPTYProc(master *os.File, cmd *exec.Cmd) (*ptyProc, error) {
	p, err := newPTYBase(master, cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	p.cmd = cmd
	go p.waitCmd()
	return p, nil
}

// adoptPTY takes a terminal and its program's PID from a kerod that
// handed them over, as an update replaces that process.
func adoptPTY(master *os.File, pid int) (*ptyProc, error) {
	p, err := newPTYBase(master, pid)
	if err != nil {
		return nil, err
	}
	go p.waitAdopted()
	return p, nil
}

func newPTYBase(master *os.File, pid int) (*ptyProc, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &ptyProc{
		master: master,
		pid:    pid,
		wakeR:  r,
		wakeW:  w,
		exited: make(chan struct{}),
	}, nil
}

func (p *ptyProc) waitCmd() {
	err := p.cmd.Wait()
	p.code = exitCode(err)
	close(p.exited)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return e.ExitCode()
	}
	return -1
}

// waitAdopted waits for the program of a terminal taken over from
// another kerod. The process is still this PID's child: exec kept the
// PID, so wait4 works as it did before the handoff. A process another
// kerod already reaped is followed until it is gone.
func (p *ptyProc) waitAdopted() {
	if proc, err := os.FindProcess(p.pid); err == nil {
		if state, err := proc.Wait(); err == nil {
			p.code = state.ExitCode()
			if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				p.code = 128 + int(ws.Signal())
			}
			close(p.exited)
			return
		}
	}
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if !processAlive(p.pid) {
			p.code = 0
			close(p.exited)
			return
		}
	}
}

func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// Read reads the program's output; io.EOF once nothing has the terminal
// open any more, or the daemon stopped reading it to hand it over.
func (p *ptyProc) Read(b []byte) (int, error) {
	p.mu.Lock()
	r, stopped := p.wakeR, p.stopped
	p.mu.Unlock()
	if stopped {
		return 0, io.EOF
	}
	fds := []unix.PollFd{
		{Fd: int32(p.master.Fd()), Events: unix.POLLIN},
		{Fd: int32(r.Fd()), Events: unix.POLLIN},
	}
	for {
		p.mu.Lock()
		stopped = p.stopped
		p.mu.Unlock()
		if stopped {
			return 0, io.EOF
		}
		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		p.mu.Lock()
		stopped = p.stopped
		p.mu.Unlock()
		if stopped {
			return 0, io.EOF
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			n, err := p.master.Read(b)
			if err != nil && n == 0 {
				if errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
					err = io.EOF
				}
			}
			return n, err
		}
		if fds[1].Revents != 0 {
			return 0, io.EOF
		}
	}
}

// stopRead stops reading the terminal without closing it, so that the
// bytes it holds wait for the kerod that takes it over.
func (p *ptyProc) stopRead() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	p.wakeW.Close()
}

// resumeRead reads the terminal again, a handoff that did not happen.
func (p *ptyProc) resumeRead() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	p.wakeR.Close()
	p.wakeR, p.wakeW = r, w
	p.stopped = false
	return nil
}

func (p *ptyProc) closeWakes() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wakeR != nil {
		p.wakeR.Close()
	}
	if p.wakeW != nil {
		p.wakeW.Close()
	}
}

func (p *ptyProc) PID() int { return p.pid }

// Kill kills the program's process group.
func (p *ptyProc) Kill() {
	if syscall.Kill(-p.pid, syscall.SIGKILL) == nil {
		return
	}
	if proc, err := os.FindProcess(p.pid); err == nil {
		proc.Kill()
	}
}
