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
// ticks interfering; zero timeout entries explicitly disable the execution deadline.
func newCronQueueTest(t *testing.T, timeouts ...int) *cronQueueTest {
	t.Helper()
	f := &cronQueueTest{started: make(chan int, 2*len(timeouts)), release: make([]chan struct{}, len(timeouts))}
	config := ScriptsConfig{Enabled: true}
	addresses := make(map[string]int)
	for i, timeout := range timeouts {
		if timeout == 0 {
			timeout = -1
		}
		config.Cron = append(config.Cron, CronScript{
			Cron: "0 0 1 1 *", Timezone: "UTC", TimeoutMS: timeout,
			Source:   "$httpClient.get({url:$argument,timeout:-1}, function() { $done(); });",
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
		t.Fatal("independent cron did not start")
		return -1
	}
}

func (f *cronQueueTest) waitIdle(t *testing.T, index int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.runner.mu.Lock()
		pending := f.runner.pending[index]
		f.runner.mu.Unlock()
		if !pending {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cron invocation did not finish")
}

func TestCronIndependentJobsAndCoalescedTriggers(t *testing.T) {
	f := newCronQueueTest(t, 0, 0, 0, 0, 0, 0)
	f.trigger()
	seen := make(map[int]bool)
	for range 6 {
		seen[f.nextStarted(t)] = true
	}
	if len(seen) != 6 {
		t.Fatal("independent jobs did not run together", seen)
	}
	for range 3 {
		f.trigger()
	}
	select {
	case index := <-f.started:
		t.Fatalf("unfinished job overlapped itself: %d", index)
	default:
	}
	f.release[0] <- struct{}{}
	f.waitIdle(t, 0)
	f.trigger()
	if index := f.nextStarted(t); index != 0 {
		t.Fatalf("completed job could not run again: %d", index)
	}
}

func TestCronInvocationTimeoutDoesNotCancelOtherJobs(t *testing.T) {
	f := newCronQueueTest(t, 100, 0, 0)
	f.trigger()
	seen := make(map[int]bool)
	for range 3 {
		seen[f.nextStarted(t)] = true
	}
	if len(seen) != 3 {
		t.Fatal("a slow job delayed another job", seen)
	}
	f.waitIdle(t, 0)
	if err := f.runner.ctx.Err(); err != nil {
		t.Fatal("one timeout stopped the scheduler", err)
	}
	f.trigger()
	if index := f.nextStarted(t); index != 0 {
		t.Fatalf("timed-out job could not run again: %d", index)
	}
}

func TestCronCloseCancelsAndWaitsForAllInvocations(t *testing.T) {
	f := newCronQueueTest(t, 0, 0, 0, 0, 0, 0)
	f.trigger()
	for range 6 {
		f.nextStarted(t)
	}
	closed := make(chan struct{})
	go func() { f.runner.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not wait for cancelled cron work")
	}
	f.trigger()
	select {
	case index := <-f.started:
		t.Fatalf("closed scheduler started job %d", index)
	default:
	}
	f.runner.mu.Lock()
	for index, pending := range f.runner.pending {
		if pending {
			t.Errorf("stop retained pending job %d", index)
		}
	}
	f.runner.mu.Unlock()
	if cronScriptsRunning.Load() != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("cron stop retained runtime or body work")
	}
}
