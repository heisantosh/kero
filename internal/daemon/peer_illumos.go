//go:build illumos || solaris

package daemon

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the user of the process at the other end of a Unix
// socket (getpeerucred), or -1.
func peerUID(c net.Conn) int {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1
	}
	uid := -1
	raw.Control(func(fd uintptr) {
		if cred, err := unix.GetPeerUcred(fd); err == nil {
			uid = int(cred.Geteuid())
		}
	})
	return uid
}
