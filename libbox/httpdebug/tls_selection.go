package httpdebug

import (
	"context"
	"fmt"
	"net"
)

// Track ClientHello inspection through handoff so service shutdown and caller
// cancellation also close probes. There is no fixed connection-count quota.
type tlsSelection struct {
	engine                 *Engine
	conn                   net.Conn
	held                   bool // protected by engine.mu
	stopCaller, stopEngine func() bool
}

type selectedTLSConn struct {
	net.Conn
	selection *tlsSelection
}

func (c *selectedTLSConn) Close() error {
	c.selection.release(false)
	return c.Conn.Close()
}

func (e *Engine) trackTLS(ctx context.Context, conn net.Conn) *tlsSelection {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	r := &tlsSelection{engine: e, conn: conn, held: true}
	e.preparing[r] = struct{}{}
	// One count for the probe and one for the pending/accepted connection.
	e.wg.Add(2)
	r.stopCaller = context.AfterFunc(ctx, func() { r.release(true) })
	r.stopEngine = context.AfterFunc(e.ctx, func() { r.release(true) })
	return r
}

func (r *tlsSelection) release(closeConn bool) {
	e := r.engine
	e.mu.Lock()
	if !r.held {
		e.mu.Unlock()
		return
	}
	r.detachLocked()
	e.mu.Unlock()
	if closeConn {
		r.conn.Close()
	}
	e.wg.Done()
}

// The caller either releases the selection's wait count or transfers it to
// serve. Stopping callbacks under the same lock prevents a late cancellation
// from closing a connection already handed back to the original route.
func (r *tlsSelection) detachLocked() {
	r.held = false
	delete(r.engine.preparing, r)
	r.stopCaller()
	r.stopEngine()
}

func (e *Engine) connectionSkipped(metadata Metadata, reason string) {
	e.skipped.Add(1)
	writeLog("warn", fmt.Sprintf("%s; original route preserved; connection=%s host=%s port=%d",
		reason, metadata.ConnectionID, metadata.Host, metadata.Port))
}
