package httpdebug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

type cronQueueTest struct {
	runner  *cronRunner
	started chan int
	release []chan struct{}
}

// Real script HTTP calls wait in the routed dialer until explicitly released
// or cancelled. Annual schedules let tests advance runDue without wall-clock
// ticks interfering; zero timeout entries use the longest allowed timeout.
func newCronQueueTest(t *testing.T, timeouts ...int) *cronQueueTest {
	t.Helper()
	f := &cronQueueTest{started: make(chan int, 2*len(timeouts)), release: make([]chan struct{}, len(timeouts))}
	config := ScriptsConfig{Enabled: true}
	addresses := make(map[string]int)
	for i, timeout := range timeouts {
		if timeout == 0 {
			timeout = 30000
		}
		config.Cron = append(config.Cron, CronScript{
			Cron: "0 0 1 1 *", Timezone: "UTC", TimeoutMS: timeout,
			Source:   "$httpClient.get($argument, function() { $done(); });",
			Argument: fmt.Sprintf("http://cron-%d.test/task", i),
		})
		addresses[fmt.Sprintf("cron-%d.test:80", i)] = i
		f.release[i] = make(chan struct{}, 1)
	}
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	f.runner = startCron(scriptProgram(t, string(content)), func(ctx context.Context, address string) (net.Conn, error) {
		index, ok := addresses[address]
		if !ok {
			return nil, fmt.Errorf("unexpected cron destination: %s", address)
		}
		select {
		case f.started <- index:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-f.release[index]:
			return nil, errors.New("cron test request released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, nil)
	t.Cleanup(f.runner.Close)
	return f
}

func (f *cronQueueTest) trigger() {
	f.runner.mu.Lock()
	now := f.runner.next[0]
	f.runner.mu.Unlock()
	f.runner.runDue(now)
}

func (f *cronQueueTest) nextStarted(t *testing.T) int {
	t.Helper()
	select {
	case index := <-f.started:
		return index
	case <-time.After(3 * time.Second):
		t.Fatal("queued cron did not start")
		return -1
	}
}

func (f *cronQueueTest) firstPair(t *testing.T) {
	t.Helper()
	first := map[int]bool{f.nextStarted(t): true, f.nextStarted(t): true}
	if len(first) != 2 || !first[0] || !first[1] {
		t.Fatalf("wrong first cron pair: %v", first)
	}
}

func TestCronQueueFIFOAndCoalescedTriggers(t *testing.T) {
	f := newCronQueueTest(t, 0, 0, 0, 0)
	f.trigger()
	f.firstPair(t)

	// Both executing and queued jobs must absorb subsequent triggers.
	f.trigger()
	if queued := len(f.runner.queue); queued != 2 {
		t.Fatalf("repeated triggers changed the backlog: %d", queued)
	}
	f.release[0] <- struct{}{}
	if index := f.nextStarted(t); index != 2 {
		t.Fatalf("third simultaneous job was skipped: got %d", index)
	}

	// Job 0 is eligible again, but must wait behind the older queued job 3.
	// Job 1 stays blocked so the other worker's dispatch order is observable.
	f.trigger()
	f.release[2] <- struct{}{}
	if index := f.nextStarted(t); index != 3 {
		t.Fatalf("a new trigger overtook an older queued job: got %d", index)
	}
	f.release[3] <- struct{}{}
	if index := f.nextStarted(t); index != 0 {
		t.Fatalf("completed job was not admitted again: got %d", index)
	}
}

func TestCronQueueWaitDoesNotConsumeExecutionTimeout(t *testing.T) {
	const timeoutMS = 200
	f := newCronQueueTest(t, 0, 0, timeoutMS)
	f.trigger()
	f.firstPair(t)
	// Hold both workers longer than the queued job's entire execution budget.
	time.Sleep(2 * timeoutMS * time.Millisecond)
	f.release[0] <- struct{}{}
	if index := f.nextStarted(t); index != 2 {
		t.Fatalf("queue waiting consumed the execution timeout: got %d", index)
	}
	f.release[2] <- struct{}{}
}

func TestCronQueueContinuesAfterInvocationTimeout(t *testing.T) {
	f := newCronQueueTest(t, 500, 0, 0)
	f.trigger()
	f.firstPair(t)
	// No request is released: job 0's timeout must free a worker for job 2.
	if index := f.nextStarted(t); index != 2 {
		t.Fatalf("timed-out invocation stalled the queue: got %d", index)
	}
	if err := f.runner.ctx.Err(); err != nil {
		t.Fatalf("one invocation cancelled the scheduler: %v", err)
	}
}

func TestCronQueueBoundAndCloseDiscardPending(t *testing.T) {
	f := newCronQueueTest(t, make([]int, MaxRewriteRules)...)
	f.trigger()
	f.firstPair(t)
	for range 8 {
		f.trigger()
	}
	if queued := len(f.runner.queue); queued != MaxRewriteRules-2 || cap(f.runner.queue) != MaxRewriteRules {
		t.Fatalf("backlog is not bounded to one invocation per job: len=%d cap=%d", queued, cap(f.runner.queue))
	}
	if running := len(cronScriptSlots); running != 2 {
		t.Fatalf("wrong number of active cron interpreters: %d", running)
	}

	closed := make(chan struct{})
	go func() {
		f.runner.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("service stop did not cancel cron workers")
	}
	f.trigger()
	select {
	case index := <-f.started:
		t.Fatalf("service stop executed a queued cron: %d", index)
	default:
	}
	if queued := len(f.runner.queue); queued != 0 {
		t.Fatalf("service stop retained %d queued jobs", queued)
	}
	f.runner.mu.Lock()
	for index, pending := range f.runner.pending {
		if pending {
			t.Errorf("service stop retained pending job %d", index)
		}
	}
	f.runner.mu.Unlock()
	if len(cronScriptSlots) != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("service stop leaked interpreter or body capacity")
	}
}
