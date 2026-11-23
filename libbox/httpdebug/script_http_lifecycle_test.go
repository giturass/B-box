package httpdebug

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptHTTPLateConn struct {
	net.Conn
	closing chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *scriptHTTPLateConn) Close() error {
	c.once.Do(func() {
		close(c.closing)
		<-c.release
		c.Conn.Close()
	})
	return nil
}

func TestHTTPClientDeadlineWaitsForRouteAndConnectionCleanup(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	closing, closeRelease, callback := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseRoute := sync.OnceFunc(func() { close(release) })
	releaseConn := sync.OnceFunc(func() { close(closeRelease) })
	clientConn, peer := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer peer.Close()
	defer clientConn.Close()
	defer releaseConn()
	defer releaseRoute()

	await := func(channel <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-channel:
		case <-time.After(3 * time.Second):
			t.Fatal(message)
		}
	}
	rule := httpAPIScript(t, HTTPScript{TimeoutMS: -1, Debug: true,
		Source: `$httpClient.get({url:'http://blocked.test/',timeout:0.5}, function(error) {
			if(!error) throw Error('missing timeout');
			console.log('request timed out');
			$done({response:{body:'deadline handled'}});
		});`})
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://intercepted.test/", nil)
	type outcome struct {
		result scriptResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := runHTTPScript(request, nil, rule, "deadline", nil, scriptEnvironment{
			dial: func(ctx context.Context, address string) (net.Conn, error) {
				if address != "blocked.test:80" {
					t.Errorf("unexpected route %s", address)
				}
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-release
				// A dialer may finish with a connection after cancellation.
				// Its bound lifetime must close that connection before join.
				return &scriptHTTPLateConn{Conn: clientConn, closing: closing, release: closeRelease}, nil
			},
		}, func(message string) {
			if strings.Contains(message, "request timed out") {
				close(callback)
			}
		})
		finished <- outcome{result, err}
	}()
	await(started, "script route did not start")
	await(cancelled, "HTTP request deadline did not cancel the route")
	await(callback, "request timeout did not reach the VM callback")

	// Waiting for one invocation's actual route must not block another VM's
	// complete HTTP request and cleanup.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("independent"))
	}))
	defer origin.Close()
	other := httpAPIScript(t, HTTPScript{Source: `$httpClient.get('http://other.test/', function(error,response,data) {
		if(error) throw Error(error); $done({response:{body:data}});
	});`})
	otherRequest, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	otherFinished := make(chan outcome, 1)
	go func() {
		result, err := runHTTPScript(otherRequest, nil, other, "independent", nil, scriptEnvironment{
			dial: func(ctx context.Context, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
			},
		}, func(string) {})
		otherFinished <- outcome{result, err}
	}()
	select {
	case output := <-otherFinished:
		if output.err != nil || output.result.response == nil {
			t.Fatal("route cleanup blocked an independent HTTP invocation", output.err)
		}
		body, err := io.ReadAll(output.result.response.Body)
		output.result.response.Body.Close()
		if err != nil || string(body) != "independent" {
			t.Fatal("independent request lost its response", string(body), err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("route cleanup blocked an independent HTTP invocation")
	}
	select {
	case <-finished:
		t.Fatal("script finished before its cancelled route exited")
	case <-time.After(100 * time.Millisecond):
	}

	releaseRoute()
	await(closing, "cancelled route did not close its late connection")
	select {
	case <-finished:
		t.Fatal("script finished before its cancelled connection closed")
	case <-time.After(100 * time.Millisecond):
	}
	releaseConn()
	select {
	case output := <-finished:
		if output.err != nil || output.result.response == nil {
			t.Fatal("request deadline callback lost its result", output.err)
		}
		body, err := io.ReadAll(output.result.response.Body)
		output.result.response.Body.Close()
		if err != nil || string(body) != "deadline handled" {
			t.Fatal("request deadline callback lost its body", string(body), err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("script did not join its finished route and connection cleanup")
	}
	if httpScriptsRunning.Load() != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("request deadline retained an interpreter or body registration")
	}
}
