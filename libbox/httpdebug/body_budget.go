package httpdebug

import (
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
)

// Track body buffers without imposing a device-independent memory quota.
// A zero limit means unlimited. Per-message limits come from the user's config;
// this counter is not a measurement of Goja allocations or process RSS.
var processingBodies bodyBudget

type bodyBudget struct {
	limit int64
	used  atomic.Int64
}

type bodyLease struct {
	budget *bodyBudget
	bytes  int64
	refs   atomic.Int32
}

func (b *bodyBudget) acquire(bytes int64) *bodyLease {
	for {
		used := b.used.Load()
		if bytes < 0 || bytes > math.MaxInt64-used || b.limit > 0 && bytes > b.limit-used {
			return nil
		}
		if b.used.CompareAndSwap(used, used+bytes) {
			lease := &bodyLease{budget: b, bytes: bytes}
			lease.refs.Store(1)
			return lease
		}
	}
}

func reserveBodyWork(size int64) *bodyLease {
	return processingBodies.acquire(size)
}

func (l *bodyLease) release() {
	if l != nil && l.refs.Add(-1) == 0 {
		l.budget.used.Add(-l.bytes)
	}
}

// Grow the reservation before copying bytes into a host buffer. Small requests
// do not reserve their configured maximum or an unmeasured copy multiplier.
// The owning network worker finishes before the lease reaches the VM callback.
func (l *bodyLease) growWork(size int64) bool {
	if size < 0 {
		return false
	}
	if l == nil {
		return true
	}
	needed := size
	if needed <= l.bytes {
		return true
	}
	delta := needed - l.bytes
	for {
		used := l.budget.used.Load()
		if delta > math.MaxInt64-used || l.budget.limit > 0 && delta > l.budget.limit-used {
			return false
		}
		if l.budget.used.CompareAndSwap(used, used+delta) {
			l.bytes = needed
			return true
		}
	}
}

// Buffered/replacement bodies retain their reservation until the consumer
// closes them, including replayed oversized responses and script mocks.
func (l *bodyLease) wrap(body io.ReadCloser) io.ReadCloser {
	if l == nil || body == nil || body == http.NoBody {
		return body
	}
	l.refs.Add(1)
	return &leasedBody{ReadCloser: body, lease: l}
}

type leasedBody struct {
	io.ReadCloser
	lease *bodyLease
	once  sync.Once
}

func (b *leasedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.lease.release)
	return err
}
