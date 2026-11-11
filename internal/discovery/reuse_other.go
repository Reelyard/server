//go:build !(linux || darwin || freebsd || openbsd || netbsd || dragonfly || windows)

package discovery

import "syscall"

func reuseControl(_, _ string, _ syscall.RawConn) error { return nil }
