package httpdebug

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestScriptHTTPAdmissionPrecedesBodyGettersAndSerialization(t *testing.T) {
	for _, test := range []struct {
		name, source string
		policy       *ScriptHTTPOptions
	}{
		{"request count", `let touched=false;
		 $httpClient.get('http://allowed.test/',()=>{});
		 $httpClient.post({url:'http://allowed.test/',get body(){touched=true;return {toJSON(){throw Error('serialized');}};}},e=>{
		 if(!e||!e.includes('request limit')||touched)throw Error('late admission'); $done();});`, &ScriptHTTPOptions{MaxRequests: 1}},
		{"destination", `$httpClient.post({url:'http://denied.test/',get body(){throw Error('body getter ran');}},e=>{
		 if(!e||!e.includes('origin'))throw Error(e||'destination allowed'); $done();});`, &ScriptHTTPOptions{AllowedOrigins: []string{"http://allowed.test"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule := httpAPIScript(t, HTTPScript{Source: test.source, HTTPClient: test.policy})
			request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
			_, err := runHTTPScript(request, nil, rule, "admission", nil, scriptEnvironment{dial: func(ctx context.Context, _ string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}}, func(string) {})
			if err != nil || processingBodies.used.Load() != 0 {
				t.Fatal("admission or cleanup failed", err)
			}
		})
	}
}

func TestScriptBodyChecksUTF8AndViewLengthBeforeHostCopy(t *testing.T) {
	vm := goja.New()
	stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	for _, test := range []struct {
		source string
		want   []byte
	}{
		{`'猫😊\ud800x\udc00'`, []byte("猫😊�x�")},
		{`new Uint8Array([99,1,2,3,98]).subarray(1,4)`, []byte{1, 2, 3}},
		{`new Uint16Array([0x0102,0x0304]).subarray(1)`, []byte{4, 3}},
	} {
		value, err := vm.RunString(test.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, deniedByBudget := range []bool{false, true} {
			budget := bodyBudget{}
			limit := int64(len(test.want) - 1)
			if deniedByBudget {
				budget.limit = limit
				limit = 0
			}
			lease := budget.acquire(0)
			body, err := scriptHTTPBody(vm, stringify, value, make(http.Header), limit, lease)
			lease.release()
			if err == nil || body != nil || budget.used.Load() != 0 {
				t.Fatal("body copied before size/admission rejection", err)
			}
		}
		budget := bodyBudget{limit: int64(len(test.want))}
		lease := budget.acquire(0)
		body, err := scriptHTTPBody(vm, stringify, value, make(http.Header), int64(len(test.want)), lease)
		if err != nil || !bytes.Equal(body, test.want) || budget.used.Load() != int64(len(body)) {
			t.Fatal("UTF-8/view length was not accounted exactly", body, err)
		}
		lease.release()
	}
}

func TestScriptNativeRegexCancellationIsPerVM(t *testing.T) {
	for _, expression := range []string{
		`/^(a+)+$/.test(input)`, `new RegExp('^(a+)+$').test(input)`,
		`(()=>{const re=/safe/;re.compile('^(a+)+$');return re.test(input);})()`,
		`input.match(/^(a+)+$/)`, `input.replace(/^(a+)+$/, 'x')`,
	} {
		t.Run(expression, func(t *testing.T) {
			program, err := goja.Compile("shared-regexp", `const input=$argument; `+expression+`; $done();`, false)
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			for _, slow := range []bool{true, false} {
				workers.Add(1)
				go func() {
					defer workers.Done()
					input := "aaaa"
					if slow {
						input = strings.Repeat("a", 100) + "!"
					}
					err := executeScript(context.Background(), program, "native-regexp", "http-request", 50, input, false,
						func(*goja.Runtime, context.Context) {}, func(*goja.Runtime, goja.Value) error { return nil })
					if slow && !errors.Is(err, context.DeadlineExceeded) || !slow && err != nil {
						t.Errorf("independent VM slow=%v: %v", slow, err)
					}
				}()
			}
			workers.Wait()
		})
	}
}

func TestHTTPScriptsHaveNoFixedVMStackOrLogQuota(t *testing.T) {
	program, err := goja.Compile("concurrent", `started(); function nested(n){return n?nested(n-1):42;} if(nested(512)!==42)throw Error('stack');
		for(let i=0;i<12;i++)console.log('x'.repeat(4096)); $done();`, false)
	if err != nil {
		t.Fatal(err)
	}
	started, release, finished := make(chan struct{}, 8), make(chan struct{}), make(chan error, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range 8 {
		go func() {
			notes := 0
			err := executeScript(ctx, program, "quotas", "http-request", 5000, "", false, func(vm *goja.Runtime, ctx context.Context) {
				_ = vm.Set("started", func() {
					started <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}, func(*goja.Runtime, goja.Value) error { return nil }, scriptRunInfo{note: func(line string) {
				notes++
				if !strings.HasSuffix(line, strings.Repeat("x", 4096)) {
					t.Error("script log truncated")
				}
			}})
			if err == nil && notes != 12 {
				err = errors.New("script logs dropped")
			}
			finished <- err
		}()
	}
	for range 8 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("scripts waited for a fixed VM quota")
		}
	}
	close(release)
	for range 8 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
}
