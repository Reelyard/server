//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseControl lets the socket share port 5353 with the system's responder.
func reuseControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr == nil {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}
