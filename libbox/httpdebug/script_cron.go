package httpdebug

import (
	"context"
	"crypto/x509"
	"errors"
	"sync"
	"time"
)

// The scheduler belongs to the local service, not to a recording Engine. A
// recording toggle never resets next-run times or creates a second scheduler.
type cronRunner struct {
	ctx     context.Context
	cancel  context.CancelFunc
	program *ScriptProgram
	tasks   *scriptTaskRunner
	owned   bool
	dial    RewriteDialFunc
	roots   *x509.CertPool
	mu      sync.Mutex
	closed  bool
	next    []time.Time
	pending []bool // A job does not overlap its own unfinished invocation.
	wg      sync.WaitGroup
}

func startCron(program *ScriptProgram, dial RewriteDialFunc, roots *x509.CertPool) *cronRunner {
	tasks := newScriptTaskRunner(context.Background(), program, scriptEnvironment{dial: dial, roots: roots, storage: program.storage, resources: program.resources})
	r := startCronForTasks(tasks)
	r.owned = true
	return r
}

func startCronForTasks(tasks *scriptTaskRunner) *cronRunner {
	ctx, cancel := context.WithCancel(tasks.ctx)
	program := tasks.program
	r := &cronRunner{ctx: ctx, cancel: cancel, program: program, tasks: tasks, dial: tasks.environment.dial, roots: tasks.environment.roots,
		next: make([]time.Time, len(program.cron)), pending: make([]bool, len(program.cron))}
	now := time.Now()
	for i, job := range program.cron {
		r.next[i] = job.schedule.Next(now)
	}
	r.wg.Add(1)
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
		// Skip missed intervals after suspend and do not overlap a job with
		// itself. Independent jobs start without an arbitrary worker quota.
		r.next[i] = job.schedule.Next(now)
		if r.pending[i] {
			writeLog("warn", job.label+": previous cron invocation is still running; skipped")
			continue
		}
		r.pending[i] = true
		r.wg.Add(1)
		_, err := r.tasks.start(r.ctx, cronTask(job), false, func() {
			r.mu.Lock()
			r.pending[i] = false
			r.mu.Unlock()
			r.wg.Done()
		})
		if err != nil {
			r.pending[i] = false
			r.wg.Done()
			if errors.Is(err, ErrScriptBusy) {
				writeLog("warn", job.label+": previous invocation is still running; cron skipped")
			}
		}
	}
}

func (r *cronRunner) run(job compiledCron) error {
	_, err := runTaskScript(r.ctx, cronTask(job), r.program.limit,
		scriptEnvironment{dial: r.dial, roots: r.roots, storage: r.program.storage, resources: r.program.resources}, false)
	return err
}

func cronTask(job compiledCron) compiledTask {
	return compiledTask{key: scriptTaskKey{"cron", job.index}, options: cronTaskOptions(job.options), label: job.label, cron: job.options.Cron,
		program: job.program, client: job.client, source: job.source, storageKey: job.storageKey}
}

func (r *cronRunner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	if r.owned {
		r.tasks.Close()
	}
	r.wg.Wait()
	r.mu.Lock()
	clear(r.pending)
	r.mu.Unlock()
}
