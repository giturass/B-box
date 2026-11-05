package httpdebug

import (
	"context"
	"crypto/x509"
	"errors"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// The scheduler belongs to the local service, not to a recording Engine. A
// recording toggle never resets next-run times or creates a second scheduler.
type cronRunner struct {
	ctx     context.Context
	cancel  context.CancelFunc
	program *ScriptProgram
	dial    RewriteDialFunc
	roots   *x509.CertPool
	mu      sync.Mutex
	closed  bool
	next    []time.Time
	pending []bool // One invocation per job, whether queued or running.
	queue   chan int
	wg      sync.WaitGroup
}

func startCron(program *ScriptProgram, dial RewriteDialFunc, roots *x509.CertPool) *cronRunner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &cronRunner{ctx: ctx, cancel: cancel, program: program, dial: dial, roots: roots,
		next: make([]time.Time, len(program.cron)), pending: make([]bool, len(program.cron)), queue: make(chan int, len(program.cron))}
	now := time.Now()
	for i, job := range program.cron {
		r.next[i] = job.schedule.Next(now)
	}
	r.wg.Add(1 + cap(cronScriptSlots))
	for range cap(cronScriptSlots) {
		go r.worker()
	}
	go r.loop()
	return r
}

func (r *cronRunner) loop() {
	defer r.wg.Done()
	for {
		now := time.Now()
		r.runDue(now)
		// Also recheck the wall clock periodically after timezone/clock changes.
		wake := now.Add(time.Minute)
		r.mu.Lock()
		for _, next := range r.next {
			if !next.IsZero() && next.Before(wake) {
				wake = next
			}
		}
		r.mu.Unlock()
		timer := time.NewTimer(max(time.Until(wake), time.Millisecond))
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *cronRunner) runDue(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return
	}
	for i, job := range r.program.cron {
		if r.next[i].IsZero() || now.Before(r.next[i]) {
			continue
		}
		// Skip missed intervals after suspend. A queued or running invocation
		// absorbs further triggers so the backlog never exceeds the job count.
		r.next[i] = job.schedule.Next(now)
		if r.pending[i] {
			writeLog("warn", job.label+": previous cron invocation is queued or still running; skipped")
			continue
		}
		select {
		case r.queue <- i:
			r.pending[i] = true
		default:
			writeLog("warn", job.label+": cron queue limit reached; skipped")
		}
	}
}

func (r *cronRunner) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case index := <-r.queue:
			// Cancellation wins even when both select cases are ready. Queue
			// waiting holds no interpreter/body budget and does not use timeout_ms.
			if r.ctx.Err() == nil {
				r.run(r.program.cron[index])
			}
			r.mu.Lock()
			r.pending[index] = false
			r.mu.Unlock()
		}
	}
}

func (r *cronRunner) run(job compiledCron) error {
	ctx, cancel := context.WithTimeout(r.ctx, time.Duration(job.options.TimeoutMS)*time.Millisecond)
	defer cancel()
	lease := reserveBodyWork(r.program.limit)
	if lease == nil {
		err := errors.New("global body budget exhausted")
		writeLog("warn", job.label+": cron skipped: "+err.Error())
		return err
	}
	defer lease.release()
	writeLog("info", job.label+": cron started")
	program := job.program
	if job.source != nil {
		value, sourceLease, err := r.program.resources.get(ctx, job.source)
		if err != nil {
			writeLog("error", job.label+": script loading: "+err.Error())
			return err
		}
		defer sourceLease.release()
		program = value.program
	}
	var cleanup = func() {}
	defer func() { cleanup() }()
	err := executeScript(ctx, program, job.label, "cron", job.options.TimeoutMS, job.options.Argument, false,
		func(vm *goja.Runtime, ctx context.Context) {
			_ = vm.Set("$cronexp", job.options.Cron)
			_ = vm.Set("$trigger", "cron")
			cleanup = installScriptHTTP(vm, ctx, r.dial, r.roots, r.program.limit, job.client)
			key := job.storageKey
			if job.source != nil {
				key = "script." + r.program.resources.key(job.source)
			}
			installScriptStorage(vm, ctx, r.program.storage, job.options.StorageNamespace, job.label, key)
		}, func(*goja.Runtime, goja.Value) error { return nil })
	if err != nil {
		writeLog("error", job.label+": cron failed: "+err.Error())
	} else {
		writeLog("info", job.label+": cron completed")
	}
	return err
}

func (r *cronRunner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
	// Producers and workers have stopped; queued triggers do not survive a
	// service stop or reload.
	r.mu.Lock()
	for len(r.queue) > 0 {
		<-r.queue
	}
	clear(r.pending)
	r.mu.Unlock()
}
