package httpdebug

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

const maxBufferedBodyBytes = 64 * 1024 * 1024

// Reserve headroom for the wire prefix, layered decoding, replacement and
// script copies before reading. This bounds admitted body work, not arbitrary
// JavaScript allocations or the entire process heap.
var processingBodies = bodyBudget{limit: maxBufferedBodyBytes}

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
		if bytes <= 0 || bytes > b.limit-used {
			return nil
		}
		if b.used.CompareAndSwap(used, used+bytes) {
			lease := &bodyLease{budget: b, bytes: bytes}
			lease.refs.Store(1)
			return lease
		}
	}
}

func reserveBodyWork(limit int64) *bodyLease {
	return processingBodies.acquire(8 * max(4096, limit+1))
}

func (l *bodyLease) release() {
	if l != nil && l.refs.Add(-1) == 0 {
		l.budget.used.Add(-l.bytes)
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
