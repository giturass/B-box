package httpdebug

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/dop251/goja"
)

const (
	maxScriptTimers       = 32
	maxScriptTimerCalls   = 128
	maxScriptTimerDelayMS = 30000
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
// a VM after $done, an error or cancellation. Host HTTP calls remain sequential;
// timer callbacks run only after the current JavaScript/host call returns.
type scriptTimers struct {
	ctx        context.Context
	pending    map[int64]*scriptTimer
	registered int64
}

func installScriptTimers(vm *goja.Runtime, ctx context.Context) *scriptTimers {
	timers := &scriptTimers{ctx: ctx, pending: make(map[int64]*scriptTimer)}
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
				panic(vm.NewTypeError("timer delay must be between 0 and 30000 milliseconds"))
			}
			if len(timers.pending) >= maxScriptTimers || timers.registered >= maxScriptTimerCalls {
				panic(vm.NewGoError(errors.New("script timer limit exceeded (32 pending, 128 registrations)")))
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
	for fired := 0; len(t.pending) > 0; fired++ {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		if fired >= maxScriptTimerCalls {
			return errors.New("script timer callback limit exceeded (128)")
		}
		var next *scriptTimer
		for _, timer := range t.pending {
			if next == nil || timer.next.Before(next.next) || timer.next.Equal(next.next) && timer.id < next.id {
				next = timer
			}
		}
		wait := time.NewTimer(max(time.Until(next.next), 0))
		select {
		case <-t.ctx.Done():
			wait.Stop()
			return t.ctx.Err()
		case <-wait.C:
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
		// Skip elapsed intervals; never queue a catch-up burst after blocking
		// HTTP, a slow callback or device suspension.
		if next.repeat {
			next.next = time.Now().Add(next.delay)
		}
	}
	return nil
}
