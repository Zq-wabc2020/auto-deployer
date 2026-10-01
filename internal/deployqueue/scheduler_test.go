package deployqueue

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSchedulerCoalesces verifies that tasks submitted while a deploy is
// running are merged: only the latest is executed, and the discarded tasks'
// authors are included in the recipients of that execution.
func TestSchedulerCoalesces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	type call struct {
		task       Task
		recipients []string
	}
	var mu sync.Mutex
	var calls []call

	started := make(chan struct{}) // exec signals it has started (and is blocking)
	proceed := make(chan struct{}) // test signals exec to return

	exec := func(ctx context.Context, task Task, recipients []string) error {
		mu.Lock()
		calls = append(calls, call{task, append([]string(nil), recipients...)})
		mu.Unlock()
		started <- struct{}{}
		<-proceed
		return nil
	}

	s := NewScheduler(exec)

	// Task A starts and blocks (deploy in progress).
	s.Submit(Task{PipelineName: "svc", AuthorEmail: "a@x.com", Branch: "main", Source: "webhook"})
	<-started // exec(A) now blocking on <-proceed

	// Tasks B and C arrive while A is still deploying.
	s.Submit(Task{PipelineName: "svc", AuthorEmail: "b@x.com", Branch: "main", Source: "webhook"})
	s.Submit(Task{PipelineName: "svc", AuthorEmail: "c@x.com", Branch: "main", Source: "webhook"})

	// Finish A: the processor should coalesce B+C into C (discard B).
	proceed <- struct{}{}
	<-started // exec(C) now blocking on <-proceed

	// Finish C.
	proceed <- struct{}{}

	// Allow the processor to drain and release the lock.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 exec calls (A then C), got %d: %+v", len(calls), calls)
	}
	if calls[0].task.AuthorEmail != "a@x.com" {
		t.Errorf("first exec should be A, got %s", calls[0].task.AuthorEmail)
	}
	if calls[1].task.AuthorEmail != "c@x.com" {
		t.Errorf("second exec should be C (latest), got %s", calls[1].task.AuthorEmail)
	}
	// First batch is just A: recipients = [a@x.com].
	assertRecipients(t, "first exec", calls[0].recipients, []string{"a@x.com"})
	// Second batch coalesced B+C: recipients = [b@x.com, c@x.com] (B discarded but notified).
	assertRecipients(t, "second exec", calls[1].recipients, []string{"b@x.com", "c@x.com"})
}

// TestSchedulerDifferentServicesParallel verifies different services run
// independently (no cross-service blocking).
func TestSchedulerDifferentServicesParallel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var mu sync.Mutex
	calls := map[string]int{}
	started := make(chan struct{}, 4)
	proceed := make(chan struct{}, 4)

	exec := func(ctx context.Context, task Task, recipients []string) error {
		mu.Lock()
		calls[task.PipelineName]++
		mu.Unlock()
		started <- struct{}{}
		<-proceed
		return nil
	}

	s := NewScheduler(exec)
	s.Submit(Task{PipelineName: "svc-a", AuthorEmail: "a@x.com", Source: "webhook"})
	s.Submit(Task{PipelineName: "svc-b", AuthorEmail: "b@x.com", Source: "webhook"})

	// Both should start without waiting on each other.
	<-started
	<-started

	proceed <- struct{}{}
	proceed <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls["svc-a"] != 1 || calls["svc-b"] != 1 {
		t.Fatalf("expected each service executed once, got %v", calls)
	}
}

// TestCollectRecipients verifies dedup and empty filtering.
func TestCollectRecipients(t *testing.T) {
	tasks := []Task{
		{AuthorEmail: "a@x.com"},
		{AuthorEmail: ""}, // manual: skipped
		{AuthorEmail: "a@x.com"}, // dup
		{AuthorEmail: "b@x.com"},
	}
	got := collectRecipients(tasks)
	want := []string{"a@x.com", "b@x.com"}
	assertRecipients(t, "collectRecipients", got, want)
}

func assertRecipients(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", label, got, want)
		return
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Errorf("%s: unexpected recipient %s", label, g)
		}
	}
}
