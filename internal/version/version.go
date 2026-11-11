// Package version holds build information, set with -ldflags at release time:
//
//	go build -ldflags "-X github.com/inluxc/harbor/internal/version.Version=v0.1.0 -X github.com/inluxc/harbor/internal/version.Commit=abc123 -X github.com/inluxc/harbor/internal/version.Date=2026-11-11"
package version

import (
	"fmt"
	"runtime"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line description of this build.
func String() string {
	return fmt.Sprintf("harbor %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
