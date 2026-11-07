package httpdebug

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func grpcTestFrame(flag byte, body []byte) []byte {
	frame := make([]byte, 5+len(body))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

// A protobuf string field grows from "old" to "new value". Test both the
// protobuf length and the enclosing gRPC length, including typed-array views.
func grpcTestScript(phase string) string {
	return `const input=$` + phase + `.body;
		if(!(input instanceof Uint8Array)) throw Error('missing binary frame');
		const payload=input[0] ? $utils.ungzip(input.subarray(5)) : input.subarray(5);
		if(!payload || payload.length!==5 || payload[0]!==10 || payload[1]!==3 || payload[2]!==111) throw Error('input protobuf');
		const value=new Uint8Array([10,9,110,101,119,32,118,97,108,117,101]);
		const output=new Uint8Array(value.length+5);
		new DataView(output.buffer).setUint32(1,value.length);
		output.set(value,5); $done({body:output});`
}

func grpcTestProgram(t *testing.T, phase, source string, limit int64) *httpProgram {
	t.Helper()
	config := ScriptsConfig{HTTP: []HTTPScript{{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: phase,
		RequiresBody: true, BinaryBody: true, GRPCMode: "unary", Source: source, MaxBodyBytes: limit}}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	program := rewriteProgram(t, `{"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"corrupt frame"},{"pattern":".","phase":"response","regex":"old","replacement":"corrupt frame"}]}`)
	program.scripts = scriptProgram(t, string(encoded))
	return program
}

func TestUnaryGRPCHTTP2RewritingPreservesTrailers(t *testing.T) {
	old := []byte{10, 3, 'o', 'l', 'd'}
	want := grpcTestFrame(0, []byte{10, 9, 'n', 'e', 'w', ' ', 'v', 'a', 'l', 'u', 'e'})
	compressed := grpcTestFrame(1, encodedBody(t, old, "gzip"))
	cert, roots := originCertificate(t, "origin.test", false)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Equal(body, want) || request.ProtoMajor != 2 || request.Trailer.Get("X-Request-End") != "kept" {
			t.Errorf("gRPC upload or trailers: %v %v %v", body, request.Trailer, err)
		}
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Grpc-Encoding", "gzip")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, X-Response-End")
		w.Write(compressed)
		w.(http.Flusher).Flush()
		w.Header().Set("Grpc-Status", "7")
		w.Header().Set("Grpc-Message", "kept%20status")
		w.Header().Set("Grpc-Status-Details-Bin", "CAc=")
		w.Header().Set("X-Response-End", "kept")
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()
	e, saved, ca := mitmEngine(t, Config{})
	e.mitm.H2 = true
	defer e.Close()
	requestProgram := grpcTestProgram(t, "request", grpcTestScript("request"), 1024)
	responseProgram := grpcTestProgram(t, "response", grpcTestScript("response"), 1024)
	e.scripts = requestProgram.scripts
	e.scripts.http = append(e.scripts.http, responseProgram.scripts.http...)
	e.rewrite = requestProgram.RewriteProgram
	client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, true)
	request, _ := http.NewRequest("POST", "https://origin.test/service/Unary", bytes.NewReader(compressed))
	request.Header.Set("Content-Type", "application/grpc+proto")
	request.Header.Set("Grpc-Encoding", "gzip")
	request.Trailer = http.Header{"X-Request-End": {"kept"}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(actual, want) || response.ProtoMajor != 2 || response.Trailer.Get("Grpc-Status") != "7" ||
		response.Trailer.Get("Grpc-Message") != "kept%20status" || response.Trailer.Get("Grpc-Status-Details-Bin") != "CAc=" || response.Trailer.Get("X-Response-End") != "kept" {
		t.Fatalf("gRPC response or trailers: %v %v %v", actual, response.Trailer, err)
	}
	detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
	if err != nil || !bytes.Equal(detail.RequestBody, want) || !bytes.Equal(detail.ResponseBody, want) || len(detail.Response.Trailers) != 4 {
		t.Fatal("capture lost rewritten frames or RPC trailers", err)
	}
}

func TestUnaryGRPCFormatsAndCompression(t *testing.T) {
	for _, mediaType := range []string{"application/grpc", "application/grpc+proto", "application/grpc-web+proto", "application/grpc-web-text+proto"} {
		for _, phase := range []string{"request", "response"} {
			for _, compressed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/compressed=%t", mediaType, phase, compressed), func(t *testing.T) {
					payload := []byte{10, 3, 'o', 'l', 'd'}
					flag := byte(0)
					if compressed {
						flag, payload = 1, encodedBody(t, payload, "gzip")
					}
					wire := grpcTestFrame(flag, payload)
					want := grpcTestFrame(0, []byte{10, 9, 'n', 'e', 'w', ' ', 'v', 'a', 'l', 'u', 'e'})
					if phase == "response" && strings.Contains(mediaType, "grpc-web") {
						trailers := grpcTestFrame(0x80, []byte("grpc-status: 0\r\nx-end: kept\r\n"))
						wire, want = append(wire, trailers...), append(want, trailers...)
					}
					if strings.Contains(mediaType, "grpc-web-text") {
						// Padding inside a frame, not merely at a frame boundary.
						wire = []byte(base64.StdEncoding.EncodeToString(wire[:2]) + "\r\n" + base64.StdEncoding.EncodeToString(wire[2:]))
						want = []byte(base64.StdEncoding.EncodeToString(want))
					}
					program := grpcTestProgram(t, phase, grpcTestScript(phase), 1024)
					request, _ := http.NewRequest("POST", "https://example.test/service/Unary", bytes.NewReader(wire))
					request.Header.Set("Content-Type", mediaType)
					request.Header.Set("Grpc-Encoding", "gzip")
					var body io.ReadCloser
					var length int64
					if phase == "request" {
						if _, err := program.request(request, "grpc", request.URL.String(), func(string) {}); err != nil {
							t.Fatal(err)
						}
						body, length = request.Body, request.ContentLength
					} else {
						response := &http.Response{StatusCode: 200, Header: request.Header.Clone(), Body: request.Body, ContentLength: int64(len(wire))}
						if err := program.response(request, response, "grpc", request.URL.String(), func(string) {}); err != nil {
							t.Fatal(err)
						}
						body, length = response.Body, response.ContentLength
					}
					actual, err := io.ReadAll(body)
					body.Close()
					if err != nil || !bytes.Equal(actual, want) || length != int64(len(want)) {
						t.Fatalf("frame changed incorrectly: %v != %v length=%d: %v", actual, want, length, err)
					}
					if processingBodies.used.Load() != 0 {
						t.Fatal("gRPC processing retained a body reservation")
					}
				})
			}
		}
	}
}

func TestUnaryGRPCNoopAndFailurePreserveWire(t *testing.T) {
	for _, source := range []string{"$done();", "throw Error('no change');"} {
		for _, mediaType := range []string{"application/grpc+proto", "application/grpc-web-text"} {
			wire := grpcTestFrame(1, encodedBody(t, []byte("original"), "gzip"))
			if mediaType == "application/grpc-web-text" {
				wire = []byte(base64.StdEncoding.EncodeToString(wire))
			}
			wire = encodedBody(t, wire, "gzip")
			program := grpcTestProgram(t, "response", source, 1024)
			request, _ := http.NewRequest("POST", "https://example.test/service/Unary", nil)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {mediaType}, "Content-Encoding": {"gzip"}, "Grpc-Encoding": {"gzip"}},
				Body: io.NopCloser(bytes.NewReader(wire)), Trailer: http.Header{"Grpc-Status": {"0"}}}
			if err := program.response(request, response, "grpc", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(actual, wire) || response.Header.Get("Content-Encoding") != "gzip" || response.Trailer.Get("Grpc-Status") != "0" {
				t.Fatal("no-op/error changed the compressed wire message", err)
			}
		}
	}
}

func TestUnaryGRPCRejectsInvalidInputWithoutRunningScript(t *testing.T) {
	frame := grpcTestFrame(0, []byte("old"))
	for _, test := range []struct {
		name, mediaType, encoding string
		body                      []byte
	}{
		{"missing header", "application/grpc", "", []byte{0, 0, 0}},
		{"wrong length", "application/grpc", "", []byte{0, 0, 0, 0, 7, 1}},
		{"reserved flag", "application/grpc", "", grpcTestFrame(2, []byte("old"))},
		{"native trailer frame", "application/grpc", "", grpcTestFrame(0x80, []byte("grpc-status: 0"))},
		{"multiple messages", "application/grpc", "", append(bytesClone(frame), frame...)},
		{"unsupported compression", "application/grpc", "snappy", grpcTestFrame(1, []byte("old"))},
		{"missing compression", "application/grpc", "", grpcTestFrame(1, []byte("old"))},
		{"damaged compression", "application/grpc", "gzip", grpcTestFrame(1, []byte("old"))},
		{"expanded limit", "application/grpc", "gzip", grpcTestFrame(1, encodedBody(t, bytes.Repeat([]byte("a"), 4096), "gzip"))},
		{"text encoding", "application/grpc-web-text", "", []byte("!invalid!")},
		{"early trailer", "application/grpc-web", "", append(grpcTestFrame(0x80, []byte("grpc-status: 0")), frame...)},
		{"trailers only", "application/grpc-web", "", grpcTestFrame(0x80, []byte("grpc-status: 7"))},
		{"headers only", "application/grpc", "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := grpcTestProgram(t, "response", `$done({headers:{"X-Ran":"unexpected"}});`, 256)
			request, _ := http.NewRequest("POST", "https://example.test/service/Unary", nil)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {test.mediaType}, "Grpc-Encoding": {test.encoding}},
				Body: io.NopCloser(bytes.NewReader(test.body)), Trailer: http.Header{"Grpc-Status": {"7"}}}
			var notes []string
			if err := program.response(request, response, "grpc", request.URL.String(), func(s string) { notes = append(notes, s) }); err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(actual, test.body) || response.Header.Get("X-Ran") != "" || response.Trailer.Get("Grpc-Status") != "7" || !strings.Contains(strings.Join(notes, " "), "gRPC unary body skipped") {
				t.Fatal("invalid unary body was modified or not diagnosed", err, notes)
			}
			if processingBodies.used.Load() != 0 {
				t.Fatal("invalid unary body retained a reservation")
			}
		})
	}
}

func TestUnaryGRPCOutputValidationAndConfiguration(t *testing.T) {
	for _, extra := range []string{`"grpc_mode":"stream","requires_body":true,"binary_body":true`, `"grpc_mode":"unary","requires_body":true`, `"grpc_mode":"unary","binary_body":true`} {
		var config ScriptsConfig
		if err := json.Unmarshal([]byte(`{"http":[{"pattern":".","phase":"response","source":"$done();",`+extra+`}]}`), &config); err == nil {
			t.Fatal("unsafe gRPC mode accepted:", extra)
		}
	}
	for _, source := range []string{
		`$done({body:new Uint8Array()});`,
		`$done({body:new Uint8Array([0,0,0,0,2,1])});`,
		`$done({body:new Uint8Array([1,0,0,0,1,1])});`,
		`$done({body:$response.body,headers:{'Content-Type':'application/json'}});`,
	} {
		program := grpcTestProgram(t, "response", source, 1024)
		request, _ := http.NewRequest("POST", "https://example.test/service/Unary", nil)
		wire := grpcTestFrame(0, []byte("old"))
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/grpc"}}, Body: io.NopCloser(bytes.NewReader(wire)), Trailer: http.Header{"Grpc-Status": {"0"}}}
		err := program.response(request, response, "grpc", request.URL.String(), func(string) {})
		actual, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if err == nil || readErr != nil || !bytes.Equal(actual, wire) || response.Trailer.Get("Grpc-Status") != "0" {
			t.Fatal("invalid output was sent or modified the original", err, readErr)
		}
	}
}

func TestUnaryGRPCBufferingCancelsAndReleasesBudget(t *testing.T) {
	program := grpcTestProgram(t, "response", "$done();", 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", "https://example.test/service/Unary", nil)
	reader, writer := io.Pipe()
	defer writer.Close()
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/grpc"}}, Body: reader}
	finished := make(chan error, 1)
	go func() { finished <- program.response(request, response, "grpc", request.URL.String(), func(string) {}) }()
	if _, err := writer.Write([]byte{0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled gRPC read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("gRPC read ignored cancellation")
	}
	response.Body.Close()
	if processingBodies.used.Load() != 0 {
		t.Fatal("cancelled gRPC read retained a reservation")
	}
}
