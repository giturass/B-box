package httpdebug

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func routedTestEnvironment(origin *httptest.Server) scriptEnvironment {
	return scriptEnvironment{dial: func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}}
}

func TestSurgeHTTPClientRunsBeyondFormerConcurrencyAndQueueLimits(t *testing.T) {
	started := make(chan struct{}, 64)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			_, _ = io.WriteString(w, "ok")
		case <-r.Context().Done():
		}
	}))
	defer origin.Close()
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for range 64 {
			select {
			case <-started:
			case <-timer.C:
				once.Do(func() { close(release) })
				return
			}
		}
		// A timer must be able to run while the HTTP operations are pending.
		time.Sleep(30 * time.Millisecond)
		once.Do(func() { close(release) })
	}()
	rule := httpAPIScript(t, HTTPScript{TimeoutMS: 5000, Source: `
		let top=false, timer=false, completed=0;
		function done(){if(top&&timer&&completed===64)$done();}
		setTimeout(()=>{timer=true;done();},0);
		for(let i=0;i<64;i++) $httpClient.get('http://parallel.test/',(error,response,data)=>{
			if(!top) throw Error('synchronous callback');
			if(error||data!=='ok')throw Error(error||'HTTP result');completed++;
			done();
		});
		top=true;
	`})
	request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, routedTestEnvironment(origin), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if processingBodies.used.Load() != 0 || httpScriptsRunning.Load() != 0 {
		t.Fatal("async client retained resources")
	}
}

func TestSurgeHTTPClientRedirectCookiesJSONBinaryAndPolicy(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(request.Body)
		switch request.URL.Path {
		case "/start":
			if request.Method != "POST" || string(body) != `{"value":42}` || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("JSON upload: %s %q %v", request.Method, body, request.Header)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "yes", Path: "/"})
			w.Header().Set("Location", "/binary")
			w.WriteHeader(307)
		case "/binary":
			if request.Method != "POST" || string(body) != `{"value":42}` || !strings.Contains(request.Header.Get("Cookie"), "session=yes") {
				t.Error("redirect lost method, body or cookie")
			}
			w.Header().Add("Set-Cookie", "first=1")
			w.Header().Add("Set-Cookie", "second=2")
			_, _ = w.Write([]byte{0, 255, 3})
		case "/typed":
			if string(body) != string([]byte{0, 255}) || request.Header.Get("Cookie") != "explicit=yes" {
				t.Errorf("typed body or auto-cookie=false: %v %v", body, request.Header)
			}
			_, _ = io.WriteString(w, "typed")
		}
	}))
	defer origin.Close()
	environment := routedTestEnvironment(origin)
	dial := environment.dial
	environment.dial = func(ctx context.Context, address string) (net.Conn, error) {
		if ScriptPolicy(ctx) != "chosen" {
			t.Error("policy was not passed to the active core dialer")
		}
		return dial(ctx, address)
	}
	rule := httpAPIScript(t, HTTPScript{Source: `
		$httpClient.post({url:'http://aux.test/start',body:{value:42},policy:'chosen','binary-mode':true,'full-header-mode':true},(error,response,data)=>{
			if(error) throw Error(error);
			if(!(data instanceof Uint8Array)||data.length!==3||data[1]!==255) throw Error('binary response');
			if(!Array.isArray(response.headers)||response.headers.filter(x=>x.field.toLowerCase()==='set-cookie').length!==2) throw Error('full headers');
			const bytes=new Uint8Array([9,0,255,8]);
			$httpClient.post({url:'http://aux.test/typed',body:bytes.subarray(1,3),policy:'chosen','auto-cookie':false,headers:{Cookie:'explicit=yes'}},(e,r,d)=>{
				if(e||d!=='typed')throw Error(e||'typed request');$done();
			});
		});
	`})
	request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, environment, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatalf("unexpected requests: %d", requests.Load())
	}
}

func TestSurgeHTTPClientAllowsMoreThanTwentySequentialCalls(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { requests.Add(1); _, _ = io.WriteString(w, "ok") }))
	defer origin.Close()
	rule := httpAPIScript(t, HTTPScript{Source: `let n=0; function next(){$httpClient.get('http://aux.test/',(e)=>{if(e)throw Error(e);if(++n===25)$done();else next();});} next();`})
	request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, routedTestEnvironment(origin), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 25 {
		t.Fatal("concurrency limit was incorrectly a total request limit")
	}
}

func TestSurgeHTTPClientInsecureAndRequestDeadline(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			<-request.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "tls")
	}))
	defer origin.Close()
	for _, source := range []string{
		`$httpClient.get({url:'https://aux.test/',insecure:true},(e,r,d)=>{if(e||d!=='tls')throw Error(e||'insecure');$done();});`,
		`$httpClient.get({url:'https://aux.test/slow',insecure:true,timeout:0.02},(e)=>{if(!e)throw Error('request timeout');$done();});`,
	} {
		rule := httpAPIScript(t, HTTPScript{Source: source})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		start := time.Now()
		if _, err := runHTTPScript(request, nil, rule, "test", nil, routedTestEnvironment(origin), func(string) {}); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("request timeout was not respected")
		}
	}
	rule := httpAPIScript(t, HTTPScript{TimeoutMS: 20, Source: `$httpClient.get({url:'https://aux.test/slow',insecure:true,timeout:10},()=>$done());`})
	request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, routedTestEnvironment(origin), func(string) {}); err == nil {
		t.Fatal("per-request timeout escaped invocation deadline")
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("timed-out requests leaked bodies")
	}
}

func TestSurgeHTTPClientCancelsPendingWorkOnExceptionAndDone(t *testing.T) {
	var active atomic.Int32
	environment := scriptEnvironment{dial: func(ctx context.Context, _ string) (net.Conn, error) {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	for _, ending := range []string{`throw Error('uncaught');`, `$done();`} {
		rule := httpAPIScript(t, HTTPScript{Source: `$httpClient.get('http://aux.test/',()=>{throw Error('late callback')});` + ending})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		_, err := runHTTPScript(request, nil, rule, "test", nil, environment, func(string) {})
		if (err != nil) != strings.HasPrefix(ending, "throw") {
			t.Fatal("wrong completion result", err)
		}
		if active.Load() != 0 || processingBodies.used.Load() != 0 {
			t.Fatal("script completion retained network work")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rule := httpAPIScript(t, HTTPScript{Source: `$done();`})
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, environment, func(string) {}); !errors.Is(err, context.Canceled) {
		t.Fatal("service cancellation was not propagated", err)
	}
}
