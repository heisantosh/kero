//go:build !linux && !darwin && !windows && !illumos && !solaris

package daemon

import "net"

func peerUID(net.Conn) int { return -1 }
