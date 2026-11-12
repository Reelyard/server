package main

import (
	"runtime"
	"strings"
	"testing"
)

// On macOS the service must see Homebrew's ffmpeg; elsewhere the service
// manager's own PATH is used.
func TestServicePath(t *testing.T) {
	for _, user := range []bool{false, true} {
		c := serviceConfig("", user)
		path := c.EnvVars["PATH"]
		if runtime.GOOS == "darwin" {
			if !strings.Contains(path, "/opt/homebrew/bin") || !strings.Contains(path, "/usr/local/bin") || !strings.HasSuffix(path, "/usr/bin:/bin:/usr/sbin:/sbin") {
				t.Fatalf("user=%v: PATH %q", user, path)
			}
		} else if c.EnvVars != nil {
			t.Fatalf("user=%v: unexpected env %v", user, c.EnvVars)
		}
	}
}
