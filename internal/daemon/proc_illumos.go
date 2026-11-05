//go:build illumos || solaris

package daemon

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// readPsinfo reads /proc/<pid>/psinfo (64-bit psinfo_t on illumos/solaris).
func readPsinfo(pid int) ([]byte, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/psinfo")
	if err != nil || len(b) < 260 {
		return nil, false
	}
	return b, true
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func procName(pid int) string {
	b, ok := readPsinfo(pid)
	if !ok {
		return ""
	}
	// pr_fname is char[16] at offset 136 in LP64 psinfo_t.
	return strings.TrimSpace(cstr(b[136:152]))
}

func procArgs(pid int) []string {
	b, ok := readPsinfo(pid)
	if !ok {
		return nil
	}
	// pr_argc (int32 at 236), pr_argv (uintptr at 240), pr_dmodel (byte at 256: 1=ILP32, 2=LP64).
	argc := int(int32(binary.LittleEndian.Uint32(b[236:240])))
	argv := binary.LittleEndian.Uint64(b[240:248])
	dmodel := b[256]
	if argc > 0 && argc <= 4096 && argv != 0 && (dmodel == 1 || dmodel == 2) {
		if as, err := os.Open("/proc/" + strconv.Itoa(pid) + "/as"); err == nil {
			defer as.Close()
			ptrSize := 8
			if dmodel == 1 {
				ptrSize = 4
			}
			ptrs := make([]byte, argc*ptrSize)
			if n, err := unix.Pread(int(as.Fd()), ptrs, int64(argv)); err == nil && n == len(ptrs) {
				args := make([]string, 0, argc)
				var buf [4096]byte
				for i := range argc {
					var addr uint64
					if ptrSize == 8 {
						addr = binary.LittleEndian.Uint64(ptrs[i*8 : (i+1)*8])
					} else {
						addr = uint64(binary.LittleEndian.Uint32(ptrs[i*4 : (i+1)*4]))
					}
					if addr == 0 {
						break
					}
					n, _ := unix.Pread(int(as.Fd()), buf[:], int64(addr))
					if n <= 0 {
						break
					}
					args = append(args, cstr(buf[:n]))
				}
				if len(args) > 0 {
					return args
				}
			}
		}
	}
	// Fallback to pr_psargs (char[80] at offset 152).
	if s := strings.TrimSpace(cstr(b[152:232])); s != "" {
		return strings.Fields(s)
	}
	return nil
}

func procCwd(pid int) string {
	dir, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/path/cwd")
	return dir
}

// procParent returns a process's parent, 0 when it is gone.
func procParent(pid int) int {
	b, ok := readPsinfo(pid)
	if !ok {
		return 0
	}
	// pr_ppid is pid_t (int32) at offset 12 in LP64 psinfo_t.
	return int(int32(binary.LittleEndian.Uint32(b[12:16])))
}
