package deploylock

import (
	"testing"
)

// TestAcquireTryAcquireRelease verifies the lock is exclusive: while held,
// TryAcquire fails; after Release it succeeds again. HOME is redirected to a
// temp dir so the lock file does not touch the real ~/.deployd.
func TestAcquireTryAcquireRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	lock, err := Acquire("svc-a")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Held by lock (same process, different fd): TryAcquire must fail.
	if _, err := TryAcquire("svc-a"); err == nil {
		t.Fatal("TryAcquire should fail while lock is held")
	}

	lock.Release()

	// After release, TryAcquire succeeds.
	lock2, err := TryAcquire("svc-a")
	if err != nil {
		t.Fatalf("TryAcquire after release failed: %v", err)
	}
	lock2.Release()
}

// TestTryAcquireDifferentServices verifies locks are per-service (independent).
func TestTryAcquireDifferentServices(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	lock, err := TryAcquire("svc-a")
	if err != nil {
		t.Fatalf("TryAcquire svc-a failed: %v", err)
	}
	defer lock.Release()

	// A different service's lock is independent and can be acquired.
	lock2, err := TryAcquire("svc-b")
	if err != nil {
		t.Fatalf("TryAcquire svc-b should succeed (different service): %v", err)
	}
	lock2.Release()
}

// TestIsHeld verifies that IsHeld reports true while a lock is held and false
// when no lock is held.
func TestIsHeld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if IsHeld("svc") {
		t.Fatal("no lock held initially")
	}
	lock, err := Acquire("svc")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if !IsHeld("svc") {
		t.Fatal("lock should be reported held")
	}
}
