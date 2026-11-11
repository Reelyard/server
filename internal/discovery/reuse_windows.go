//go:build windows

package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// reuseControl lets the socket share port 5353 with the system's responder.
func reuseControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
