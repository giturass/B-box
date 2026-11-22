package httpdebug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dop251/goja"
)

var (
	ErrScriptBusy        = errors.New("script task is already running")
	ErrScriptsInactive   = errors.New("scripts require the running local service")
	ErrScriptUnavailable = errors.New("script task is unavailable or disabled")
)

// Result is JSON text, rather than an exported Goja value tied to a finished VM.
// Logs contain this invocation's console output independently of log filtering.
type ScriptRunResult struct {
	Result string   `json:"result"`
	Error  string   `json:"error"`
	Logs   []string `json:"logs"`
}

type ScriptTask struct {
	cancel   context.CancelFunc
	complete chan struct{}
	output   ScriptRunResult // Published by closing complete.
}

func (t *ScriptTask) Cancel() { t.cancel() }
func (t *ScriptTask) Close()  { t.Cancel() }
func (t *ScriptTask) Join()   { <-t.complete }

// Wait joins source loading, JavaScript, timers and the routed HTTP workers.
// Cancellation does not release service lifetime registration before they exit.
func (t *ScriptTask) Wait() ScriptRunResult {
	t.Join()
	output := t.output
	output.Logs = slices.Clone(output.Logs)
	return output
}

// One runner belongs to one activated profile. Automatic Cron/event and manual
// invocations share admission, task identity and the service shutdown wait.
type scriptTaskRunner struct {
	ctx         context.Context
	cancel      context.CancelFunc
	program     *ScriptProgram
	environment scriptEnvironment
	mu          sync.Mutex
	closed      bool
	running     map[scriptTaskKey]bool
	wg          sync.WaitGroup
}

func newScriptTaskRunner(parent context.Context, program *ScriptProgram, environment scriptEnvironment) *scriptTaskRunner {
	ctx, cancel := context.WithCancel(parent)
	return &scriptTaskRunner{ctx: ctx, cancel: cancel, program: program, environment: environment, running: make(map[scriptTaskKey]bool)}
}

// RunScript starts an enabled Cron, event or generic task from the currently
// activated configuration. Index is its original position in the kind's array.
// The platform also checks the profile revision before calling this entrypoint.
func RunScript(ctx context.Context, kind string, index int) (*ScriptTask, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if profileProcessing == nil || profileProcessing.tasks == nil {
		return nil, ErrScriptsInactive
	}
	return profileProcessing.tasks.runManual(ctx, kind, index)
}

func (p *PreparedProcessing) RunScript(ctx context.Context, kind string, index int) (*ScriptTask, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if p == nil || profileProcessing != p || p.tasks == nil {
		return nil, ErrScriptsInactive
	}
	return p.tasks.runManual(ctx, kind, index)
}

func (r *scriptTaskRunner) runManual(ctx context.Context, kind string, index int) (*ScriptTask, error) {
	job, found := r.program.tasks[scriptTaskKey{kind, index}]
	if !found {
		return nil, ErrScriptUnavailable
	}
	return r.start(ctx, job, true, nil)
}

func (r *scriptTaskRunner) start(parent context.Context, job compiledTask, manual bool, finished func()) (*ScriptTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, ErrScriptsInactive
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if r.running[job.key] {
		return nil, ErrScriptBusy
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.ctx, cancel)
	task := &ScriptTask{cancel: cancel, complete: make(chan struct{})}
	r.running[job.key] = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer stop()
		defer cancel()
		task.output, _ = runTaskScript(ctx, job, r.program.limit, r.environment, manual)
		r.mu.Lock()
		delete(r.running, job.key)
		r.mu.Unlock()
		if finished != nil {
			finished()
		}
		close(task.complete)
	}()
	return task, nil
}

func (r *scriptTaskRunner) notify(event string) error {
	if !validScriptEvent(event) {
		return fmt.Errorf("unsupported script event %q", event)
	}
	r.mu.Lock()
	closed := r.closed || r.ctx.Err() != nil
	r.mu.Unlock()
	if closed {
		return ErrScriptsInactive
	}
	for _, job := range r.program.event {
		if job.event != event {
			continue
		}
		if _, err := r.start(r.ctx, job, false, nil); err != nil {
			if errors.Is(err, ErrScriptBusy) {
				writeLog("warn", job.label+": previous invocation is still running; event skipped")
				continue
			}
			return err
		}
	}
	return nil
}

// NotifyScriptEvent only registers background invocations. It never waits for
// JavaScript on a network callback or the service lifecycle goroutine.
func NotifyScriptEvent(event string) error {
	managerMu.Lock()
	defer managerMu.Unlock()
	if profileProcessing == nil || profileProcessing.tasks == nil {
		return ErrScriptsInactive
	}
	return profileProcessing.tasks.notify(event)
}

// A callback retains its prepared profile, so a copied callback from an old
// interface monitor cannot deliver an event to the replacement service.
func (p *PreparedProcessing) NotifyScriptEvent(event string) error {
	if p == nil {
		return ErrScriptsInactive
	}
	runner := p.notifications.Load()
	if runner == nil {
		return ErrScriptsInactive
	}
	return runner.notify(event)
}

func (r *scriptTaskRunner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}

func runTaskScript(parent context.Context, job compiledTask, bodyLimit int64, environment scriptEnvironment, manual bool) (output ScriptRunResult, err error) {
	output = ScriptRunResult{Result: "null", Logs: []string{}}
	ctx, cancel := scriptContext(parent, job.options.TimeoutMS)
	defer cancel()
	invocation := job.key.kind
	if manual {
		invocation = "manual " + invocation
	}
	writeLog("info", job.label+": "+invocation+" started")
	defer func() {
		if err != nil {
			output.Result = "null"
			output.Error = err.Error()
			writeLog("error", job.label+": "+invocation+" failed: "+err.Error())
		} else {
			writeLog("info", job.label+": "+invocation+" completed")
		}
	}()
	program := job.program
	if job.source != nil {
		if environment.resources == nil {
			return output, fmt.Errorf("%s: script loading requires the running local service", job.label)
		}
		var value processingResourceValue
		var lease *bodyLease
		value, lease, err = environment.resources.get(ctx, job.source)
		if err != nil {
			return output, fmt.Errorf("%s: script loading: %w", job.label, err)
		}
		defer lease.release()
		program = value.program
	}
	cleanup := func() {}
	defer func() { cleanup() }()
	var stringify goja.Callable
	err = executeScript(ctx, program, job.label, job.key.kind, job.options.TimeoutMS, job.options.Argument, false,
		func(vm *goja.Runtime, ctx context.Context) {
			// Capture the native serializer before script globals can replace it.
			stringify, _ = goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
			if manual {
				_ = vm.Set("$trigger", "editor")
			}
			if job.key.kind == "cron" {
				_ = vm.Set("$cronexp", job.cron)
			} else if job.key.kind == "event" {
				name := job.event
				if manual {
					name = "manually"
				}
				_ = vm.Set("$event", map[string]any{"name": name, "data": map[string]any{}})
			}
			cleanup = installScriptHTTP(vm, ctx, environment.dial, environment.roots, bodyLimit, job.client)
			key := job.storageKey
			if job.source != nil {
				key = "script." + environment.resources.key(job.source)
			}
			installScriptStorage(vm, ctx, environment.storage, job.options.StorageNamespace, job.label, key)
		}, func(_ *goja.Runtime, value goja.Value) error {
			if !manual {
				return nil
			}
			if missingScriptValue(value) {
				output.Result = "null"
				return nil
			}
			encoded, err := stringify(goja.Undefined(), value)
			if err != nil {
				return fmt.Errorf("$done result must be JSON serializable: %w", err)
			}
			if missingScriptValue(encoded) || !json.Valid([]byte(encoded.String())) {
				return errors.New("$done result must be JSON serializable")
			}
			output.Result = encoded.String()
			return nil
		}, scriptRunInfo{name: job.options.Name, note: func(line string) {
			if manual {
				output.Logs = append(output.Logs, line)
			}
		}})
	return output, err
}
