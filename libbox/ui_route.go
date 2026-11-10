package libbox

import (
	"context"
	"net"
	"sync"
)

// UI clients share the same admission and shutdown boundary. Cancelling an
// HTTP request can finish before DNS/routing has unwound; the service must wait
// for those calls before closing the instance they use.
type uiRouteLifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc
	access sync.Mutex
	calls  sync.WaitGroup
}

func newUIRouteLifecycle(parent context.Context) *uiRouteLifecycle {
	ctx, cancel := context.WithCancel(parent)
	return &uiRouteLifecycle{ctx: ctx, cancel: cancel}
}

func (r *uiRouteLifecycle) begin(parent context.Context) (context.Context, func(), error) {
	r.access.Lock()
	defer r.access.Unlock()
	if err := r.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.ctx, cancel)
	r.calls.Add(1)
	return ctx, sync.OnceFunc(func() { stop(); cancel() }), nil
}

func (r *uiRouteLifecycle) route(parent context.Context, route func(context.Context, net.Conn, func(error))) (net.Conn, error) {
	ctx, cancel, err := r.begin(parent)
	if err != nil {
		return nil, err
	}
	local, routed := net.Pipe()
	stop := context.AfterFunc(ctx, func() { local.Close(); routed.Close() })
	closeConnection := sync.OnceFunc(func() {
		stop()
		cancel()
		local.Close()
		routed.Close()
	})
	go func() {
		defer r.calls.Done()
		if ctx.Err() != nil {
			closeConnection()
			return
		}
		route(ctx, routed, func(error) { closeConnection() })
	}()
	return &uiRoutedConn{Conn: local, close: closeConnection}, nil
}

// A script's explicit outbound bypasses RouteConnectionEx, but its synchronous
// DialContext still accesses the same instance and must join the shutdown wait.
func (r *uiRouteLifecycle) dial(parent context.Context, dial func(context.Context) (net.Conn, error)) (net.Conn, error) {
	ctx, cancel, err := r.begin(parent)
	if err != nil {
		return nil, err
	}
	defer r.calls.Done()
	conn, err := dial(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		if conn != nil {
			conn.Close()
		}
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return &uiRoutedConn{Conn: conn, close: sync.OnceFunc(func() {
		stop()
		cancel()
		conn.Close()
	})}, nil
}

func (r *uiRouteLifecycle) close() {
	if r != nil {
		r.access.Lock()
		r.cancel()
		r.access.Unlock()
	}
}

func (r *uiRouteLifecycle) wait() {
	if r != nil {
		r.calls.Wait()
	}
}

type uiRoutedConn struct {
	net.Conn
	close func()
}

func (c *uiRoutedConn) Close() error {
	c.close()
	return nil
}
