//go:build illumos || solaris

package daemon

import (
	"os"
	"path/filepath"
)

// init points the terminal plugin at the libghostty-vt installed beside
// kero. The plugin's embedded manifest has no illumos entry, and the
// server archive carries the library next to the executable.
func init() {
	if os.Getenv("MYGO_GHOSTTY_VT") != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	lib := filepath.Join(filepath.Dir(exe), "libghostty-vt.so")
	if _, err := os.Stat(lib); err == nil {
		os.Setenv("MYGO_GHOSTTY_VT", lib)
	}
}
