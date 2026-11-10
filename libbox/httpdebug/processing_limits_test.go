package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/itchyny/gojq"
)

func TestProcessingConfigurationHasNoFormerCountOrSizeCaps(t *testing.T) {
	ports := make([]uint16, 33)
	for i := range ports {
		ports[i] = uint16(i + 1)
	}
	packages, hosts := make([]string, 257), make([]string, 257)
	resources := make(map[string]RewriteResource)
	for i := range packages {
		packages[i] = fmt.Sprintf("com.example.app%d", i)
		hosts[i] = fmt.Sprintf("host%d.example", i)
		resources[fmt.Sprint(i)] = RewriteResource{DataType: "text", Data: "$done();"}
	}
	rewrite := RewriteConfig{Ports: ports, Packages: packages, Resources: resources, MaxRequestBodyBytes: 1 << 40, MaxResponseBodyBytes: -1}
	for range 257 {
		rewrite.MapLocal = append(rewrite.MapLocal, MapLocal{RewriteMatch: RewriteMatch{Pattern: "."}, Data: "ok"})
	}
	program, err := CompileRewrite(&rewrite)
	if err != nil || program.requestLimit != 1<<40 || program.limit != 0 {
		t.Fatal("rewrite count or explicit limits changed", err)
	}
	scripts := ScriptsConfig{Resources: resources, MaxBodyBytes: -1}
	for range 257 {
		scripts.HTTP = append(scripts.HTTP, HTTPScript{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "request", Source: "$done();", TimeoutMS: -1})
	}
	compiled, err := CompileScripts(&scripts, time.UTC)
	if err != nil || compiled.limit != 0 || len(compiled.http) != 257 || compiled.http[0].options.TimeoutMS != -1 {
		t.Fatal("script count or unlimited options changed", err)
	}
	mitm := MITMConfig{Hosts: hosts, Ports: ports, Packages: packages}
	if err := mitm.validate(); err != nil {
		t.Fatal("large MITM scope rejected", err)
	}
	for _, config := range []any{&rewrite, &scripts, &Config{Ports: ports, Packages: packages, BodyLimit: -1}} {
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		data = append(bytes.Repeat([]byte(" "), 2*1024*1024+1), data...)
		if err = json.Unmarshal(data, config); err != nil {
			t.Fatal("large valid configuration rejected", err)
		}
	}
}

func TestRewriteRegexAcceptsLongPatternsDenseAndSparseGroups(t *testing.T) {
	for _, test := range []struct{ pattern, input, replacement, want string }{
		{strings.Repeat("a", 4097), strings.Repeat("a", 4097), "matched", "matched"},
		{strings.Repeat("(a)", 40) + "(?=!)", strings.Repeat("a", 40) + "!", "$40", "a!"},
		{`(?<100000>a)(?=!)`, "a!", "${100000}", "a!"},
	} {
		re, err := compileRewritePattern(test.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := boundedReplace(re, test.input, test.replacement, 0)
		if err != nil || got != test.want {
			t.Fatal("pattern/group was limited or incorrectly expanded", err)
		}
	}
}

func TestJQHasNoOutputCountDepthOrImplicitDeadline(t *testing.T) {
	compile := func(source string) *gojq.Code {
		query, err := gojq.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		code, err := gojq.Compile(query)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	data, err := rewriteJSONBody(context.Background(), compile("range(8193)"), []byte("0"), 0)
	if err != nil || len(bytes.Split(data, []byte{'\n'})) != 8193 {
		t.Fatal("jq output count was capped", err)
	}
	data, err = rewriteJSONBody(context.Background(), compile("reduce range(257) as $i (0; [.])"), []byte("0"), 0)
	if err != nil || string(data) != strings.Repeat("[", 257)+"0"+strings.Repeat("]", 257) {
		t.Fatal("jq nesting was capped", err)
	}
	if _, err = rewriteJSONBody(context.Background(), compile(`"expanded"`), []byte("0"), 4); !errors.Is(err, errRewriteSize) {
		t.Fatal("explicit jq output limit lost", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = rewriteJSONBody(ctx, compile("def f: f; f"), []byte("0"), 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("jq did not observe caller cancellation", err)
	}
}

func TestCaptureRetainsLargeBodiesAndAllRecordsUntilClear(t *testing.T) {
	store, request := bodyTestStore(t)
	id := store.begin(Metadata{}, request, -1)
	body := bytes.Repeat([]byte("x"), 33*1024*1024)
	store.appendBody(id, false, body)
	store.bodyComplete(id, false)
	store.finish(id)
	ids := make([]string, 0, 301)
	for range 301 {
		next := store.begin(Metadata{}, request, -1)
		store.finish(next)
		ids = append(ids, next)
	}
	reopened, err := OpenStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if page := reopened.Page(0, ""); page.Total != 302 || len(page.Items) != 302 || page.StoredBytes <= 32*1024*1024 {
		t.Fatal("capture history was evicted", page.Total)
	}
	detail, err := reopened.Detail(id)
	if err != nil || !bytes.Equal(detail.RequestBody, body) || detail.Request.BodyTruncated || detail.RequestPreview.Truncated {
		t.Fatal("unlimited captured body changed on reopen", err)
	}
	var output bytes.Buffer
	if err := reopened.WriteHARSelection(&output, ids); err != nil {
		t.Fatal("selection beyond 300 failed", err)
	}
	var document struct {
		Log struct{ Entries []json.RawMessage }
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil || len(document.Log.Entries) != 301 {
		t.Fatal("large HAR selection was truncated", err)
	}
	headers, truncated := captureHeaders(http.Header{"X-Large": {strings.Repeat("x", 65537)}}, "")
	if truncated || len(headers) != 1 || len(headers[0].Value) != 65537 {
		t.Fatal("recorded headers were truncated")
	}
	reopened.Clear()
	if reopened.Page(0, "").Total != 0 {
		t.Fatal("explicit clear did not delete history")
	}
}
