package goja

import "sync/atomic"

// Compiled programs are shared by independent VMs. Bind cancellation only to
// the runtime's copy, using the same uncatchable interrupt as the bytecode loop.
func (r *Runtime) interruptibleRegexp(pattern *regexpPattern) *regexpPattern {
	copy := pattern.clone()
	copy.regexp2Wrapper.rx = copy.regexp2Wrapper.rx.WithCancelCheck(func() error {
		vm := r.vm
		if atomic.LoadUint32(&vm.interrupted) == 0 {
			return nil
		}
		vm.interruptLock.Lock()
		err := &InterruptedError{iface: vm.interruptVal}
		err.stack = vm.captureStack(nil, 0)
		vm.interruptLock.Unlock()
		panic(err)
	})
	return copy
}
