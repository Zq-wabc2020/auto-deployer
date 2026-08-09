// Package deployqueue provides a per-service coalescing deploy queue.
//
// Multiple webhook triggers for the same service are serialized and merged:
// when the processor is ready, all pending tasks are drained and only the
// latest is executed; the rest are discarded (logged to the service log, and
// their operators are included in the deployment notification recipients).
//
// Concurrency safety across the daemon and forked manual deploys is provided
// by a per-service file lock (internal/deploylock): the queue processor takes
// it (blocking) and keeps it while the queue is non-empty, so a manual deploy's
// non-blocking try-lock correctly observes "busy" whenever there is pending or
// in-flight work for that service.
package deployqueue

import (
	"context"
	"sync"

	"github.com/auto-deployer/auto-deployer/internal/deploylock"
	"github.com/auto-deployer/auto-deployer/internal/logger"
)

// Task represents a deploy request submitted to the queue.
type Task struct {
	ServiceName string
	Branch      string
	RepoURL     string
	AuthorEmail string // operator: webhook commit author; "" for manual
	Source      string // "webhook" | "manual"
}

// ExecFunc executes a deploy for the given task with the merged operator emails
// as notification recipients.
type ExecFunc func(ctx context.Context, task Task, operatorEmails []string) error

// queueCapacity bounds the number of pending tasks buffered per service. When
// full, additional submits are dropped (the latest commit is still deployed via
// fetch; only that author may miss the notification).
const queueCapacity = 256

// Scheduler holds a per-service coalescing queue.
type Scheduler struct {
	mu     sync.Mutex
	queues map[string]*serviceQueue
	exec   ExecFunc
}

// NewScheduler creates a Scheduler that uses exec to run deploys.
func NewScheduler(exec ExecFunc) *Scheduler {
	return &Scheduler{queues: make(map[string]*serviceQueue), exec: exec}
}

// Submit enqueues a deploy task for the service. Non-blocking: if the
// per-service buffer is full the task is dropped with a log warning.
func (s *Scheduler) Submit(task Task) {
	s.getOrCreate(task.ServiceName).submit(task)
}

// Pending returns the number of tasks queued (not yet executing) for a service.
func (s *Scheduler) Pending(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[name]
	if !ok {
		return 0
	}
	return len(q.ch)
}

func (s *Scheduler) getOrCreate(name string) *serviceQueue {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q, ok := s.queues[name]; ok {
		return q
	}
	q := &serviceQueue{ch: make(chan Task, queueCapacity), exec: s.exec}
	s.queues[name] = q
	go q.run()
	return q
}

type serviceQueue struct {
	ch   chan Task
	exec ExecFunc
}

func (q *serviceQueue) submit(task Task) {
	select {
	case q.ch <- task:
		logger.GetServiceLogger(task.ServiceName).Printf("[queue] 任务入队，待处理 %d", len(q.ch))
	default:
		logger.GetServiceLogger(task.ServiceName).Printf(
			"[queue] 队列已满，丢弃任务 (branch %s, by %s, via %s)",
			task.Branch, opLabel(task), task.Source)
	}
}

// run is the per-service processor. It blocks until a task arrives, takes the
// deploy lock, then coalesces+executes as long as tasks keep arriving, keeping
// the lock held the whole time. The lock is released only when the queue drains,
// so "busy" (pending or in-flight) is always reflected to manual try-locks.
func (q *serviceQueue) run() {
	for {
		first := <-q.ch
		lock, err := deploylock.Acquire(first.ServiceName)
		if err != nil {
			logger.GetServiceLogger(first.ServiceName).Printf("[queue] 获取部署锁失败: %v", err)
			continue
		}
		q.processWithLock(first, lock)
	}
}

// processWithLock coalesces and executes tasks while holding the lock. It
// returns (releasing the lock) only when the queue is empty.
func (q *serviceQueue) processWithLock(first Task, lock *deploylock.Lock) {
	defer lock.Release()
	current := first
	for {
		tasks := drain(q.ch, current)
		latest := tasks[len(tasks)-1]
		discarded := tasks[:len(tasks)-1]
		log := logger.GetServiceLogger(latest.ServiceName)
		log.Printf("[queue] 开始执行 (合并 %d 个任务，丢弃 %d)", len(tasks), len(discarded))
		for _, d := range discarded {
			log.Printf("[queue] 跳过部署 (branch %s, by %s, via %s) - 合并到更新任务",
				d.Branch, opLabel(d), d.Source)
		}
		if err := q.exec(context.Background(), latest, collectRecipients(tasks)); err != nil {
			log.Printf("[queue] 部署失败: %v", err)
		}
		// Non-blocking check for more tasks. If one arrived, keep the lock and
		// coalesce again; otherwise release the lock and wait for the next submit.
		select {
		case current = <-q.ch:
			continue
		default:
			return
		}
	}
}

// drain returns first plus all tasks currently buffered, in arrival order
// (oldest first). Non-blocking after the first.
func drain(ch <-chan Task, first Task) []Task {
	tasks := []Task{first}
	for {
		select {
		case t := <-ch:
			tasks = append(tasks, t)
		default:
			return tasks
		}
	}
}

// collectRecipients returns the deduplicated, non-empty author emails from tasks
// (discarded + latest), so every operator whose push was folded into the deploy
// is notified of the result.
func collectRecipients(tasks []Task) []string {
	seen := make(map[string]bool, len(tasks))
	var recipients []string
	for _, t := range tasks {
		if t.AuthorEmail == "" || seen[t.AuthorEmail] {
			continue
		}
		seen[t.AuthorEmail] = true
		recipients = append(recipients, t.AuthorEmail)
	}
	return recipients
}

func opLabel(t Task) string {
	if t.AuthorEmail != "" {
		return t.AuthorEmail
	}
	return "manual"
}
