package httpdebug

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runCronHTTPResponseScript(t *testing.T, origin *httptest.Server, limit int64, source string) {
	t.Helper()
	program, err := CompileScripts(&ScriptsConfig{MaxBodyBytes: limit, Cron: []CronScript{{
		Cron: "0 2 * * *", Source: source, TimeoutMS: 5000, HTTPClient: &ScriptHTTPOptions{MaxBodyBytes: limit},
	}}}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	runner := &cronRunner{ctx: context.Background(), program: program, dial: func(ctx context.Context, address string) (net.Conn, error) {
		if address != "encoded.test:80" {
			t.Errorf("unexpected routed address %q", address)
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}}
	if err = runner.run(program.cron[0]); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 1 {
		t.Fatalf("expected one routed request, got %d", dials.Load())
	}
	if processingBodies.used.Load() != 0 || cronScriptsRunning.Load() != 0 {
		t.Fatal("Cron HTTP retained body or interpreter capacity")
	}
}

func TestCronHTTPClientDecodesCompressedResponses(t *testing.T) {
	const limit = 512
	for _, test := range []struct {
		name     string
		encoders []string
		headers  []string
		data     []byte
	}{
		{"plain", nil, nil, nil},
		{"identity", nil, []string{"identity"}, nil},
		{"gzip", []string{"gzip"}, []string{"gzip"}, nil},
		{"x-gzip", []string{"gzip"}, []string{"x-gzip"}, nil},
		{"deflate", []string{"deflate"}, []string{"deflate"}, nil},
		{"raw-deflate", []string{"raw-deflate"}, []string{"deflate"}, nil},
		{"brotli", []string{"br"}, []string{"br"}, nil},
		{"zstd", []string{"zstd"}, []string{"zstd"}, nil},
		{"comma-separated", []string{"gzip", "br"}, []string{"gzip, br"}, nil},
		{"repeated-fields", []string{"gzip", "br", "zstd"}, []string{"gzip", "br", "zstd"}, nil},
		{"mixed-case-identity", []string{"gzip", "br"}, []string{" GZip , identity", " BR "}, nil},
		{"empty-gzip", []string{"gzip"}, []string{"gzip"}, []byte{}},
		{"exact-decoded-limit", []string{"gzip"}, []string{"gzip"}, bytes.Repeat([]byte("a"), limit)},
		{"exact-wire-limit", nil, nil, bytes.Repeat([]byte("a"), limit)},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := test.data
			if plain == nil {
				plain = []byte(`{"message":"压缩响应"}`)
			}
			wire := encodedBody(t, plain, test.encoders...)
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("explicit-accept-encoding=%t", explicit), func(t *testing.T) {
					accept := ""
					options := `{url:'http://encoded.test/'}`
					if explicit {
						accept = "gzip, deflate, br, zstd"
						options = `{url:'http://encoded.test/',headers:{'Accept-Encoding':'gzip, deflate, br, zstd'}}`
					}
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Accept-Encoding") != accept {
							t.Errorf("changed Accept-Encoding: %q", r.Header.Get("Accept-Encoding"))
						}
						for _, field := range test.headers {
							w.Header().Add("Content-Encoding", field)
						}
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", strconv.Itoa(len(wire)))
						w.Header().Set("ETag", `"representation"`)
						w.Write(wire)
					}))
					defer origin.Close()
					headers := `if(response.headers['Content-Encoding']!==undefined || response.headers['Content-Length']!==undefined) throw Error('stale compressed headers');`
					if len(test.encoders) == 0 {
						encoding := "undefined"
						if len(test.headers) != 0 {
							encoding = strconv.Quote(test.headers[0])
						}
						headers = fmt.Sprintf(`if(response.headers['Content-Encoding']!==%s || response.headers['Content-Length']!==%q) throw Error('changed uncompressed headers');`, encoding, strconv.Itoa(len(wire)))
					}
					source := fmt.Sprintf(`$httpClient.get(%s,function(error,response,data) {
						if(error) throw Error(error);
						if(response.status!==200 || data!==%q) throw Error('wrong decoded response');
						if(response.headers['Content-Type']!=='application/json' || response.headers['Etag']!=='"representation"') throw Error('lost response headers');
						%s
						$done();
					});`, options, string(plain), headers)
					runCronHTTPResponseScript(t, origin, limit, source)
				})
			}
		})
	}
}

func TestCronHTTPClientRejectsInvalidCompressedResponses(t *testing.T) {
	const limit = 512
	gzipBody := encodedBody(t, []byte("ok"), "gzip")
	corruptGzip := append([]byte(nil), gzipBody...)
	corruptGzip[len(corruptGzip)-8] ^= 1
	// The final plaintext is empty, but the intermediate concatenation of
	// gzip members exceeds the limit and must not be fully materialized.
	intermediate := bytes.Repeat(encodedBody(t, nil, "gzip"), 64)
	stacked := encodedBody(t, intermediate, "gzip")
	if len(intermediate) <= limit || len(stacked) > limit {
		t.Fatal("invalid intermediate expansion fixture")
	}
	for _, test := range []struct {
		name    string
		headers []string
		wire    []byte
		limit   int64
		failure string
	}{
		{"unknown", []string{"future"}, []byte("valid UTF-8"), limit, "unsupported Content-Encoding"},
		{"unknown-repeated", []string{"gzip", "future"}, gzipBody, limit, "unsupported Content-Encoding"},
		{"too-many-layers", []string{"gzip, identity", "identity, identity"}, gzipBody, limit, "three layers"},
		{"invalid-gzip", []string{"gzip"}, []byte("valid UTF-8"), limit, "decompression failed"},
		{"empty-gzip-stream", []string{"gzip"}, nil, limit, "decompression failed"},
		{"truncated-gzip", []string{"gzip"}, gzipBody[:len(gzipBody)-4], limit, "decompression failed"},
		{"corrupt-gzip-checksum", []string{"gzip"}, corruptGzip, limit, "decompression failed"},
		{"invalid-deflate", []string{"deflate"}, []byte{0xff}, limit, "decompression failed"},
		{"invalid-brotli", []string{"br"}, []byte{0xff}, limit, "decompression failed"},
		{"invalid-zstd", []string{"zstd"}, []byte("invalid"), limit, "decompression failed"},
		{"wire-limit", []string{"gzip"}, gzipBody, int64(len(gzipBody) - 1), "response exceeds max_body_bytes"},
		{"decoded-limit", []string{"gzip"}, encodedBody(t, bytes.Repeat([]byte("a"), limit+1), "gzip"), limit, "decoded response exceeds max_body_bytes"},
		{"intermediate-limit", []string{"gzip", "gzip"}, stacked, limit, "decoded response exceeds max_body_bytes"},
		{"decoded-binary", []string{"gzip"}, encodedBody(t, []byte{0xff}, "gzip"), limit, "must be UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, field := range test.headers {
					w.Header().Add("Content-Encoding", field)
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(test.wire)))
				w.Write(test.wire)
			}))
			defer origin.Close()
			source := fmt.Sprintf(`$httpClient.get('http://encoded.test/',function(error,response,data) {
				if(typeof error!=='string' || !error.includes(%q) || response!==null || data!==undefined) throw Error('wrong failure: '+error);
				$done();
			});`, test.failure)
			runCronHTTPResponseScript(t, origin, test.limit, source)
		})
	}
}

func TestCronHTTPClientPreservesBodylessResponseMetadata(t *testing.T) {
	for _, test := range []struct {
		method   string
		status   int
		encoding string
	}{
		{http.MethodHead, http.StatusOK, "gzip"},
		{http.MethodHead, http.StatusOK, "future"},
		{http.MethodGet, http.StatusNoContent, "gzip"},
		{http.MethodGet, http.StatusNotModified, "br"},
	} {
		t.Run(fmt.Sprintf("%s-%d-%s", test.method, test.status, test.encoding), func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method {
					t.Errorf("unexpected method %s", r.Method)
				}
				w.Header().Set("Content-Encoding", test.encoding)
				if test.method == http.MethodHead {
					w.Header().Set("Content-Length", "1024")
				}
				w.WriteHeader(test.status)
			}))
			defer origin.Close()
			source := fmt.Sprintf(`$httpClient.%s('http://encoded.test/',function(error,response,data) {
				if(error) throw Error(error);
				if(response.status!==%d || data!=='' || response.headers['Content-Encoding']!==%q) throw Error('wrong bodyless response');
				if(%q==='HEAD' && response.headers['Content-Length']!=='1024') throw Error('lost representation length');
				$done();
			});`, strings.ToLower(test.method), test.status, test.encoding, test.method)
			runCronHTTPResponseScript(t, origin, 1, source)
		})
	}
}
