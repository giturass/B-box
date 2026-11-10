package httpdebug

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestScriptTimersOrderArgumentsIntervalsAndDone(t *testing.T) {
	for _, source := range []string{
		`setTimeout($done,0);`,
		`for(let i=0;i<129;i++) setTimeout(()=>{},86400001); $done();`,
		`let n=0; for(let i=0;i<129;i++) setTimeout(()=>{if(++n===129)$done();},0);`,
		`let phase='top'; setTimeout((a,b)=>{if(phase!=='returned'||a!==3||b!=='arg') throw Error('arguments or order'); $done();},0,3,'arg'); phase='returned';`,
		`let n=0; const id=setInterval(()=>{if(++n===2){clearInterval(id); setTimeout(()=>{if(n!==2) throw Error('not cleared'); $done();},5);}},1);`,
		`const a=setTimeout(()=>{throw Error('timeout survived clear');},0); clearInterval(a);
			const b=setInterval(()=>{throw Error('interval survived clear');},0); clearTimeout(b);
			clearTimeout(); clearInterval(999); setTimeout(()=>{$done();},5);`,
		`let order=[]; setTimeout(()=>{order.push(1); setTimeout(()=>{order.push(3); $done({response:{body:order.join(',')}});},1);},0);
			setTimeout(()=>{order.push(2);},0);`,
		`setTimeout(()=>{throw Error('survived done');},30000); $done();`,
		`setTimeout(()=>{throw Error('survived done');},86400000); $done();`,
		`for(let i=0;i<129;i++) clearTimeout(setTimeout(()=>{},100)); $done();`,
		`setTimeout(()=>{$done(); throw Error('after done');},0);`,
	} {
		rule := httpAPIScript(t, HTTPScript{Source: source})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		start := time.Now()
		result, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if result.response != nil {
			body := make([]byte, 5)
			if n, _ := result.response.Body.Read(body); n != 5 || string(body) != "1,2,3" {
				t.Fatalf("timer ordering: %q", body)
			}
			result.response.Body.Close()
		}
		if time.Since(start) > time.Second {
			t.Fatal("$done waited for pending timers")
		}
	}
}

func TestScriptTimersValidateDelaysAndCancel(t *testing.T) {
	for _, test := range []struct {
		source, message string
		timeout         int
	}{
		{`setTimeout(()=>{throw Error('timer failure');},0);`, "timer failure", 2000},
		{`setTimeout('eval is not allowed',0); $done();`, "function", 2000},
		{`setTimeout(()=>{},NaN); $done();`, "delay", 2000},
		{`setTimeout(()=>{},Infinity); $done();`, "delay", 2000},
		{`setTimeout(()=>{},-1); $done();`, "delay", 2000},
		{`setTimeout(()=>{},1e100); $done();`, "delay", 2000},
		{`setInterval(()=>{},0);`, "deadline", 20},
		{`setTimeout(()=>{$done();},1000);`, "deadline", 20},
		{`setTimeout(()=>{for(;;){}},0);`, "deadline", 20},
		{`const id=setTimeout(()=>{$done();},0); clearTimeout(id);`, "must call", 2000},
	} {
		rule := httpAPIScript(t, HTTPScript{Source: test.source, TimeoutMS: test.timeout})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		_, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{}, func(string) {})
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("timer failure %q: %v", test.message, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	rule := httpAPIScript(t, HTTPScript{Source: "setTimeout(()=>{$done();},30000);"})
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://intercepted.test/", nil)
	if _, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{}, func(string) {}); err == nil {
		t.Fatal("cancelled timer succeeded")
	}
	if httpScriptsRunning.Load() != 0 {
		t.Fatal("timer cancellation retained an interpreter")
	}
}

func TestCronTimersUseTheSameInvocationDeadline(t *testing.T) {
	content, _ := json.Marshal(ScriptsConfig{Cron: []CronScript{{
		Cron: "* * * * *", TimeoutMS: 1000,
		Source: "setTimeout(()=>{if($script.type!=='cron') throw Error('context'); $done();},1);",
	}}})
	program := scriptProgram(t, string(content))
	runner := &cronRunner{ctx: context.Background(), program: program}
	if err := runner.run(program.cron[0]); err != nil {
		t.Fatal(err)
	}
	if cronScriptsRunning.Load() != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("cron timer retained resources")
	}
}
