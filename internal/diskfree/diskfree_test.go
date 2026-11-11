package diskfree

import "testing"

func TestFree(t *testing.T) {
	n, err := Free(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("Free = %d, %v", n, err)
	}
	if _, err := Free("/definitely/not/here"); err == nil {
		t.Fatal("missing path accepted")
	}
}
