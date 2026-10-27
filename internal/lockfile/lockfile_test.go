package lockfile

import (
	"path/filepath"
	"testing"
)

func TestAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := Acquire(path, "1 here")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, "2 there"); err == nil {
		t.Fatal("a second holder got the lock")
	} else if holder, held := IsHeld(err); !held || holder != "1 here" {
		t.Errorf("IsHeld = %q, %v; want the first holder", holder, held)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(path, "2 there")
	if err != nil {
		t.Fatalf("after the first let go: %v", err)
	}
	_ = again.Close()
}
