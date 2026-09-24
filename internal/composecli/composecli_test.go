package composecli

import (
	"sync"
	"testing"
)

// TestUnlockWithoutLockDoesNotPanic covers the error path where Unlock runs
// before a successful TryLock. close(nil-channel) would panic and take down
// the process.
func TestUnlockWithoutLockDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unlock on an unheld lock panicked: %v", r)
		}
	}()

	Unlock("never-locked-stack")
}

// TestUnlockTwiceDoesNotPanic guards the double-close path.
func TestUnlockTwiceDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Unlock panicked: %v", r)
		}
	}()

	if !TryLock("double-unlock-stack") {
		t.Fatal("TryLock on a free stack should succeed")
	}
	Unlock("double-unlock-stack")
	Unlock("double-unlock-stack")
}

// TestTryLockSerializesConcurrentTryLock checks the lock actually blocks a
// second acquirer and that Unlock re-enables acquisition.
func TestTryLockSerializesConcurrentTryLock(t *testing.T) {
	const name = "concurrent-stack"

	if !TryLock(name) {
		t.Fatal("first TryLock should succeed")
	}
	if TryLock(name) {
		t.Fatal("second TryLock on a held stack should fail")
	}

	// Concurrent attempts while held must all fail.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if TryLock(name) {
				t.Error("concurrent TryLock on a held stack should fail")
			}
		}()
	}
	wg.Wait()

	Unlock(name)
	if !TryLock(name) {
		t.Fatal("TryLock after Unlock should succeed")
	}
	Unlock(name)
}
