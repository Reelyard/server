// Package diskfree reports free space on the disk holding a path.
package diskfree

// Free returns the bytes available to this user on the filesystem that
// holds path.
func Free(path string) (uint64, error) { return free(path) }
