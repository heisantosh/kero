//go:build windows

package daemon

import "os"

const handoffSupported = false

func (d *Daemon) prepareHandoff() (string, error) { return "", errNoHandoff }
func (d *Daemon) execHandoff(string)              {}
func (d *Daemon) resumeHandoff()                  {}

var errNoHandoff = errorString("kerod does not hand its sessions over on Windows")

type errorString string

func (e errorString) Error() string { return string(e) }

func adoptPTY(*os.File, int) (*ptyProc, error) { return nil, errNoHandoff }
