//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const handoffSupported = true

// prepareHandoff stops the panes' readers, writes their screens and the
// state the next kerod adopts, and checks that the next binary can read
// it. It returns the file to hand over.
func (d *Daemon) prepareHandoff() (string, error) {
	d.mu.Lock()
	panes := make([]*pane, 0, len(d.panes))
	for _, p := range d.panes {
		panes = append(panes, p)
	}
	d.mu.Unlock()

	live := make([]*pane, 0, len(panes))
	for _, p := range panes {
		if p.beginHandoff() {
			live = append(live, p)
		}
	}
	for _, p := range live {
		p.waitRead()
	}

	d.saveState()
	d.saveHistories(true)

	dir := d.handoffDir()
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		d.resumeHandoff()
		return "", err
	}
	st := &handoffState{Version: handoffVersion}
	for _, p := range panes {
		if p.isBrowser() {
			continue
		}
		hp, err := p.snapshotForHandoff(dir)
		if err != nil {
			d.resumeHandoff()
			return "", err
		}
		st.Panes = append(st.Panes, hp)
	}
	path := d.handoffPath()
	if err := writeHandoff(path, st); err != nil {
		d.resumeHandoff()
		return "", err
	}
	if err := d.checkHandoff(path); err != nil {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		d.resumeHandoff()
		return "", err
	}
	return path, nil
}

// checkHandoff runs the binary that follows this kerod on the handoff it
// will read, before this one is replaced and cannot run it again.
func (d *Daemon) checkHandoff(path string) error {
	exe := d.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	out, err := exec.Command(exe, "__handoff-check", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s __handoff-check: %w (%s)", exe, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// execHandoff replaces this kerod with the binary installed, which
// inherits the PTY masters and adopts the panes. It returns only on
// failure, and then the old kerod keeps its sessions.
func (d *Daemon) execHandoff(path string) {
	exe := d.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			d.log.Printf("handoff: %v", err)
			d.resumeHandoff()
			return
		}
	}
	if err := syscall.Exec(exe, os.Args, handoffEnv(path)); err != nil {
		d.log.Printf("handoff: exec %s: %v", exe, err)
		d.resumeHandoff()
	}
}

func handoffEnv(path string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "KERO_HANDOFF" || k == "KERO_HANDOFF_FILE" || k == takeoverEnv {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "KERO_HANDOFF=1", "KERO_HANDOFF_FILE="+path)
}

// resumeHandoff reads the panes again, after a handoff that did not
// happen: an exec that failed, or a file the next binary cannot read.
func (d *Daemon) resumeHandoff() {
	d.mu.Lock()
	panes := make([]*pane, 0, len(d.panes))
	for _, p := range d.panes {
		panes = append(panes, p)
	}
	d.mu.Unlock()
	for _, p := range panes {
		p.resumeHandoff()
	}
	_ = os.Remove(d.handoffPath())
	_ = os.RemoveAll(d.handoffDir())
}

// beginHandoff stops the pane's reader, reporting whether one ran. The
// bytes in the terminal stay in the kernel for the next kerod.
func (s *pane) isBrowser() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.Browser()
}

func (s *pane) beginHandoff() (live bool) {
	s.mu.Lock()
	if s.closed || s.proc == nil {
		s.mu.Unlock()
		return false
	}
	s.handingOff = true
	proc := s.proc
	s.mu.Unlock()
	proc.stopRead()
	return true
}

func (s *pane) waitRead() {
	s.mu.Lock()
	ch := s.readDone
	s.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
	}
}

func (s *pane) resumeHandoff() {
	s.mu.Lock()
	if !s.handingOff {
		s.mu.Unlock()
		return
	}
	s.handingOff = false
	proc := s.proc
	s.readDone = make(chan struct{})
	s.mu.Unlock()
	if proc == nil {
		return
	}
	if err := proc.resumeRead(); err != nil {
		s.d.log.Printf("handoff: reading %s again: %v", s.info.ID, err)
		return
	}
	go s.pump(proc)
}

func (s *pane) snapshotForHandoff(dir string) (handoffPane, error) {
	s.mu.Lock()
	info := s.info
	info.Restored = false
	awaiting := s.awaiting
	proc := s.proc
	exited := proc == nil
	code := info.ExitCode
	snap := []byte(nil)
	if s.term != nil {
		snap = s.term.Snapshot()
	}
	s.mu.Unlock()

	hp := handoffPane{
		ID: info.ID, Info: info, Exited: exited, Code: code, Awaiting: awaiting,
	}
	if len(snap) > 0 {
		path := filepath.Join(dir, filepath.Base(info.ID)+".snap")
		if err := writeAtomic(path, snap); err != nil {
			return hp, err
		}
		hp.Snapshot = path
	}
	if proc != nil {
		hp.PID = proc.PID()
		hp.FD = int(proc.master.Fd())
		if err := clearCloexec(proc.master); err != nil {
			return hp, err
		}
	}
	return hp, nil
}

func clearCloexec(f *os.File) error {
	_, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0)
	return err
}
