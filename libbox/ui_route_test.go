package libbox

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestUIRouteShutdownWaitsForRoutingAndPolicyDials(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(map[bool]string{false: "router", true: "policy"}[policy], func(t *testing.T) {
			routes := newUIRouteLifecycle(context.Background())
			entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			defer routes.close()
			callDone := make(chan error, 1)
			var peer net.Conn
			if policy {
				local, remote := net.Pipe()
				peer = remote
				defer peer.Close()
				go func() {
					conn, err := routes.dial(context.Background(), func(ctx context.Context) (net.Conn, error) {
						close(entered)
						<-ctx.Done()
						close(cancelled)
						<-release
						return local, nil // A late success must be closed after cancellation.
					})
					if conn != nil {
						conn.Close()
						t.Error("late policy dial escaped shutdown")
					}
					callDone <- err
				}()
			} else {
				conn, err := routes.route(context.Background(), func(ctx context.Context, conn net.Conn, onClose func(error)) {
					close(entered)
					<-ctx.Done()
					close(cancelled)
					<-release
					onClose(nil)
				})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("routing did not start")
			}
			routes.close()
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("routing did not receive cancellation")
			}
			waited := make(chan struct{})
			go func() { routes.wait(); close(waited) }()
			select {
			case <-waited:
				t.Fatal("shutdown returned while instance routing was still executing")
			case <-time.After(20 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			select {
			case <-waited:
			case <-time.After(3 * time.Second):
				t.Fatal("routing wait did not finish")
			}
			if policy {
				if err := <-callDone; !errors.Is(err, context.Canceled) {
					t.Fatal("late dial lost cancellation", err)
				}
				_ = peer.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := peer.Read(make([]byte, 1)); err == nil {
					t.Fatal("late connection stayed open")
				}
			}
			if conn, err := routes.route(context.Background(), func(context.Context, net.Conn, func(error)) { t.Error("route admitted after shutdown") }); err == nil || conn != nil {
				t.Fatal("closed routing lifecycle accepted work")
			}
		})
	}
}

func TestUIRouteAdmissionRacesWithClose(t *testing.T) {
	for range 20 {
		routes := newUIRouteLifecycle(context.Background())
		start := make(chan struct{})
		var workers sync.WaitGroup
		for range 16 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				conn, err := routes.route(context.Background(), func(ctx context.Context, conn net.Conn, onClose func(error)) { <-ctx.Done(); onClose(ctx.Err()) })
				if err == nil {
					conn.Close()
				}
			}()
		}
		close(start)
		routes.close()
		routes.wait()
		workers.Wait()
		routes.wait()
	}
}
