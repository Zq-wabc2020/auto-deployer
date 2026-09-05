// internal/servstate/state_test.go
package servstate

import (
	"os"
	"testing"
)

func TestWriteAndReadStarting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteStarting("svc1", "build"); err != nil {
		t.Fatal(err)
	}
	st, ok := Read("svc1")
	if !ok || st.Status != "starting" || st.Stage != "build" {
		t.Fatalf("got %+v ok=%v", st, ok)
	}
	if st.Since.IsZero() {
		t.Error("Since should be set")
	}
}

func TestWriteFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := WriteFailed("svc1"); err != nil {
		t.Fatal(err)
	}
	st, _ := Read("svc1")
	if st.Status != "start_failed" {
		t.Fatalf("got %q", st.Status)
	}
}

func TestClearRemovesFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = WriteStarting("svc1", "fetch")
	_ = Clear("svc1")
	if _, ok := Read("svc1"); ok {
		t.Fatal("state should be cleared")
	}
}

func TestCorruptStateReadsAsNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = WriteStarting("svc1", "fetch")
	// 写坏 JSON → 应降级为无状态（走实时探测）
	if err := os.WriteFile(Path("svc1"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read("svc1"); ok {
		t.Fatal("corrupt state should read as absent (degrade to live probe)")
	}
}

func TestCancelSentinel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if HasCancel("svc1") {
		t.Fatal("no sentinel yet")
	}
	if err := WriteCancel("svc1"); err != nil {
		t.Fatal(err)
	}
	if !HasCancel("svc1") {
		t.Fatal("sentinel should exist")
	}
	if err := ClearCancel("svc1"); err != nil {
		t.Fatal(err)
	}
	if HasCancel("svc1") {
		t.Fatal("sentinel should be cleared")
	}
}
