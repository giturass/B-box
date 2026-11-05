package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/dop251/goja"
)

const (
	maxScriptTimers       = 64
	maxScriptTimerDelayMS = 24 * 60 * 60 * 1000
)

type scriptTimer struct {
	id        int64
	next      time.Time
	delay     time.Duration
	repeat    bool
	callback  goja.Callable
	arguments []goja.Value
}

// Timers belong to a single invocation. No timer or callback goroutine retains
// a VM after $done, an error or cancellation. HTTP workers enqueue events;
// all callbacks run only after the current JavaScript/host call returns.
type scriptTimers struct {
	ctx        context.Context
	pending    map[int64]*scriptTimer
	registered int64
	events     chan scriptHTTPEvent
}

type scriptLoopKey struct{}

type scriptHTTPEvent struct {
	call    func() error
	release func()
}

func installScriptTimers(vm *goja.Runtime, ctx context.Context) *scriptTimers {
	timers := &scriptTimers{ctx: ctx, pending: make(map[int64]*scriptTimer), events: make(chan scriptHTTPEvent, maxScriptHTTPConcurrent*2)}
	for _, name := range []string{"setTimeout", "setInterval"} {
		_ = vm.Set(name, func(call goja.FunctionCall) goja.Value {
			callback, ok := goja.AssertFunction(call.Argument(0))
			if !ok {
				panic(vm.NewTypeError("%s requires a function", name))
			}
			delay := float64(0)
			if value := call.Argument(1); !missingScriptValue(value) {
				if value.ExportType() != reflect.TypeFor[int64]() && value.ExportType() != reflect.TypeFor[float64]() {
					panic(vm.NewTypeError("timer delay must be a number"))
				}
				delay = value.ToFloat()
			}
			if math.IsNaN(delay) || math.IsInf(delay, 0) || delay < 0 || delay > maxScriptTimerDelayMS {
				panic(vm.NewTypeError("timer delay must be between 0 and 86400000 milliseconds"))
			}
			if len(timers.pending) >= maxScriptTimers {
				panic(vm.NewGoError(errors.New("script timer limit exceeded (64 pending)")))
			}
			timers.registered++
			duration := max(time.Duration(delay*float64(time.Millisecond)), time.Millisecond)
			var arguments []goja.Value
			if len(call.Arguments) > 2 {
				arguments = append([]goja.Value(nil), call.Arguments[2:]...)
			}
			timer := &scriptTimer{id: timers.registered, next: time.Now().Add(duration),
				delay: duration, repeat: name == "setInterval", callback: callback, arguments: arguments}
			timers.pending[timer.id] = timer
			return vm.ToValue(timer.id)
		})
	}
	clear := func(call goja.FunctionCall) goja.Value {
		value := call.Argument(0)
		if value.ExportType() == reflect.TypeFor[int64]() || value.ExportType() == reflect.TypeFor[float64]() {
			id := value.ToInteger()
			if float64(id) == value.ToFloat() {
				delete(timers.pending, id)
			}
		}
		return goja.Undefined()
	}
	_ = vm.Set("clearTimeout", clear)
	_ = vm.Set("clearInterval", clear)
	return timers
}

func (t *scriptTimers) run() error {
	for {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		var next *scriptTimer
		for _, timer := range t.pending {
			if next == nil || timer.next.Before(next.next) || timer.next.Equal(next.next) && timer.id < next.id {
				next = timer
			}
		}
		var wait *time.Timer
		var ready <-chan time.Time
		if next != nil {
			wait = time.NewTimer(max(time.Until(next.next), 0))
			ready = wait.C
		}
		select {
		case <-t.ctx.Done():
			if wait != nil {
				wait.Stop()
			}
			return fmt.Errorf("script must call $done() before its deadline: %w", t.ctx.Err())
		case event := <-t.events:
			if wait != nil {
				wait.Stop()
			}
			err := func() error { defer event.release(); return event.call() }()
			if err != nil {
				return err
			}
			continue
		case <-ready:
		}
		if err := t.ctx.Err(); err != nil {
			return err
		}
		if !next.repeat {
			delete(t.pending, next.id)
		}
		if _, err := next.callback(goja.Undefined(), next.arguments...); err != nil {
			return err
		}
		// Skip elapsed intervals; never queue a catch-up burst after a slow
		// callback or device suspension.
		if next.repeat {
			next.next = time.Now().Add(next.delay)
		}
	}
}
