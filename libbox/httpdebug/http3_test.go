//go:build with_quic

package httpdebug

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
)

func http3TestOrigin(t *testing.T, certificate tls.Certificate, handler http.Handler) string {
	t.Helper()
	raw, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate}, NextProtos: []string{http3.NextProtoH3}}, Handler: handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(raw) }()
	t.Cleanup(func() {
		_ = raw.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("HTTP/3 origin did not stop")
		}
	})
	return raw.LocalAddr().String()
}

func http3TestClient(t *testing.T, e *Engine, origin string, ca, roots *x509.CertPool, version quic.Version, clientTLS *tls.Config) (*http.Client, *atomic.Int32) {
	t.Helper()
	dials := &atomic.Int32{}
	if clientTLS == nil {
		clientTLS = &tls.Config{RootCAs: ca, ServerName: "origin.test", MinVersion: tls.VersionTLS13}
	}
	transport := &http3.Transport{TLSClientConfig: clientTLS,
		QUICConfig:         &quic.Config{Versions: []quic.Version{version}, HandshakeIdleTimeout: time.Second},
		DisableCompression: true}
	transport.Dial = func(ctx context.Context, _ string, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
		inbound, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		if !e.HandleQUIC(context.Background(), inbound, Metadata{ConnectionID: "quic-original-connection", Host: "origin.test", Port: 443,
			Protocol: "tls", sniffedSNI: true, Source: "192.0.2.2:4321", Destination: "198.51.100.9:443",
			Outbound: "selected-udp-route", Rule: "selected-rule", UID: 1234, Packages: []string{"com.test"}},
			func(callCtx context.Context) (net.PacketConn, net.Addr, error) {
				dials.Add(1)
				if err := callCtx.Err(); err != nil {
					return nil, nil, err
				}
				raw, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					return nil, nil, err
				}
				peer, err := net.ResolveUDPAddr("udp", origin)
				if err != nil {
					_ = raw.Close()
					return nil, nil, err
				}
				return raw, peer, nil
			}, nil, roots, nil) {
			_ = inbound.Close()
			return nil, errors.New("QUIC was not selected")
		}
		raw, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			_ = inbound.Close()
			return nil, err
		}
		qt := &quic.Transport{Conn: raw}
		conn, err := qt.Dial(ctx, inbound.LocalAddr(), tlsConfig, config)
		if err != nil {
			_ = raw.Close()
			_ = qt.Close()
			return nil, err
		}
		context.AfterFunc(conn.Context(), func() { _ = raw.Close(); _ = qt.Close() })
		return conn, nil
	}
	t.Cleanup(func() { _ = transport.Close() })
	return &http.Client{Transport: transport, Timeout: 4 * time.Second}, dials
}

func TestHTTP3MITMRewritesScriptsMultiplexingAndHAR(t *testing.T) {
	for _, version := range []quic.Version{quic.Version1, quic.Version2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			certificate, roots := originCertificate(t, "origin.test", false)
			var originRequests atomic.Int32
			origin := http3TestOrigin(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originRequests.Add(1)
				if r.ProtoMajor != 3 || r.TLS == nil || r.TLS.NegotiatedProtocol != "h3" {
					t.Errorf("origin request was not HTTP/3: %s %+v", r.Proto, r.TLS)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Add("Set-Cookie", "a=1")
				w.Header().Add("Set-Cookie", "b=2")
				if r.URL.Path == "/rewrite" {
					if string(body) != "after-script" || r.Header.Get("X-Rewritten") != "yes" {
						t.Errorf("HTTP/3 request processing was skipped: %q %v", body, r.Header)
					}
					_, _ = io.WriteString(w, "source")
					return
				}
				w.Header().Set("Trailer", "X-Upstream-Trailer")
				_, _ = w.Write(body)
				w.Header().Set("X-Upstream-Trailer", "retained")
			}))
			e, store, ca := mitmEngine(t, Config{BodyLimit: -1})
			e.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"],"h3":true,"block_quic":true}`)
			e.rewrite = rewriteProgram(t, `{"enabled":true,"max_body_bytes":-1,"header_rewrite":[{"pattern":"/rewrite$","phase":"request","operation":"add","header":"X-Rewritten","value":"yes"}],"body_rewrite":[{"pattern":"/rewrite$","phase":"request","regex":"before","replacement":"after"},{"pattern":"/rewrite$","phase":"response","regex":"source","replacement":"changed"}]}`).RewriteProgram
			e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":"/rewrite$","phase":"request","requires_body":true,"source":"$done({body:$request.body+'-script'})"},{"pattern":"/rewrite$","phase":"response","requires_body":true,"source":"$done({status:202,body:$response.body+'-script'})"}]}`)
			if !e.H3Enabled() || e.RejectQUIC(Metadata{Host: "origin.test", Port: 443}) {
				t.Fatal("HTTP/3 enabled profile still uses QUIC blocking")
			}
			client, dials := http3TestClient(t, e, origin, ca, roots, version, nil)
			response, err := client.Post("https://origin.test/rewrite", "text/plain", strings.NewReader("before"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || string(body) != "changed-script" || response.StatusCode != 202 || response.ProtoMajor != 3 || len(response.Header.Values("Set-Cookie")) != 2 {
				t.Fatalf("HTTP/3 pipeline failed: %d %s %q %v", response.StatusCode, response.Proto, body, err)
			}
			const streams = 6
			var concurrent sync.WaitGroup
			failures := make(chan error, streams)
			for index := 0; index < streams; index++ {
				concurrent.Add(1)
				go func(index int) {
					defer concurrent.Done()
					payload := strings.Repeat(fmt.Sprintf("stream-%d-", index), 32*1024)
					response, err := client.Post(fmt.Sprintf("https://origin.test/large/%d", index), "application/octet-stream", strings.NewReader(payload))
					if err != nil {
						failures <- err
						return
					}
					body, err := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if err != nil || string(body) != payload || response.Trailer.Get("X-Upstream-Trailer") != "retained" {
						failures <- fmt.Errorf("stream %d lost body/trailers: bytes=%d err=%v trailer=%v", index, len(body), err, response.Trailer)
					}
				}(index)
			}
			concurrent.Wait()
			close(failures)
			for failure := range failures {
				t.Error(failure)
			}
			items := waitComplete(t, store, streams+1)
			ids := make(map[string]bool)
			for _, item := range items {
				ids[item.ID] = true
				detail, err := store.detailWithBodies(item.ID)
				if err != nil {
					t.Fatal(err)
				}
				metadata := detail.Metadata
				if !strings.HasPrefix(detail.Request.Protocol, "HTTP/3") || !strings.HasPrefix(detail.Response.Protocol, "HTTP/3") ||
					metadata.ConnectionID != "quic-original-connection" || metadata.Outbound != "selected-udp-route" || metadata.Rule != "selected-rule" || metadata.UID != 1234 ||
					metadata.TLS == nil || metadata.TLS.ALPN != "h3" || metadata.TLS.UpstreamALPN == nil || *metadata.TLS.UpstreamALPN != "h3" ||
					metadata.TLS.ClientVersion != "TLS 1.3" || metadata.TLS.UpstreamVersion != "TLS 1.3" || len(metadata.TLS.UpstreamCertificate) != 64 ||
					!detail.Request.BodyComplete || !detail.Response.BodyComplete {
					t.Errorf("HTTP/3 capture lost protocol, route, TLS or body state: %+v", detail)
				}
				if strings.HasSuffix(detail.Request.URL, "/rewrite") && (string(detail.RequestBody) != "after-script" || string(detail.ResponseBody) != "changed-script") {
					t.Errorf("HTTP/3 saved pre-processing bodies: %+v", detail)
				}
			}
			if len(ids) != streams+1 || dials.Load() != 1 || originRequests.Load() != streams+1 {
				t.Fatalf("HTTP/3 did not reuse/multiplex one upstream: IDs=%d dials=%d requests=%d", len(ids), dials.Load(), originRequests.Load())
			}
			var har bytes.Buffer
			if err := store.WriteHAR(&har, ""); err != nil {
				t.Fatal(err)
			}
			var document struct {
				Log struct {
					Entries []struct {
						Request struct {
							HTTPVersion string `json:"httpVersion"`
						} `json:"request"`
						Response struct {
							HTTPVersion string `json:"httpVersion"`
						} `json:"response"`
					} `json:"entries"`
				} `json:"log"`
			}
			if err := json.Unmarshal(har.Bytes(), &document); err != nil || len(document.Log.Entries) != streams+1 {
				t.Fatal("HTTP/3 exchanges missing from HAR", err)
			}
			for _, entry := range document.Log.Entries {
				if !strings.HasPrefix(entry.Request.HTTPVersion, "HTTP/3") || !strings.HasPrefix(entry.Response.HTTPVersion, "HTTP/3") {
					t.Error("HAR rewrote the negotiated HTTP/3 version")
				}
			}
		})
	}
}

func TestHTTP3OfflineMockRecordingAndAuthority(t *testing.T) {
	e, store, ca := mitmEngine(t, Config{})
	e.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"],"h3":true}`)
	e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[{"pattern":"/static$","status":201,"data":"offline"}]}`).RewriteProgram
	e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":"/dynamic$","phase":"request","source":"$done({response:{status:202,body:'script-offline'}})"}]}`)
	e.recording.Store(nil)
	client, dials := http3TestClient(t, e, "127.0.0.1:1", ca, nil, quic.Version1, nil)
	for _, path := range []string{"static", "dynamic"} {
		response, err := client.Get("https://origin.test/" + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.ProtoMajor != 3 || !strings.Contains(string(body), "offline") {
			t.Fatal("HTTP/3 offline mock failed", response.Proto, string(body), err)
		}
	}
	if dials.Load() != 0 || store.Page(10, "").Total != 0 {
		t.Fatal("HTTP/3 offline processing dialed an upstream or ignored stopped recording")
	}
	config := Config{}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	e.recording.Store(&config)
	response, err := client.Get("https://origin.test/static")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	request, _ := http.NewRequest("GET", "https://origin.test/static", nil)
	request.Host = "other.test"
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMisdirectedRequest || dials.Load() != 0 {
		t.Fatal("HTTP/3 accepted a cross-authority request", response.StatusCode, dials.Load())
	}
	for _, item := range waitComplete(t, store, 2) {
		detail, err := store.Detail(item.ID)
		if err != nil || detail.Metadata.TLS == nil || detail.Metadata.TLS.UpstreamALPN != nil || detail.Metadata.TLS.UpstreamVersion != "" || detail.Metadata.TLS.UpstreamCertificate != "" {
			t.Fatal("HTTP/3 mock fabricated upstream TLS information", detail, err)
		}
	}
}

func TestHTTP3ServerCertificateValidationAndClientTrust(t *testing.T) {
	for _, test := range []struct {
		name, host                                             string
		expired, untrusted, skip, clientUntrusted, wantSuccess bool
	}{
		{name: "trusted", host: "origin.test", wantSuccess: true},
		{name: "expired", host: "origin.test", expired: true},
		{name: "wrong-name", host: "wrong.test"},
		{name: "untrusted-origin", host: "origin.test", untrusted: true},
		{name: "explicit-skip", host: "wrong.test", expired: true, untrusted: true, skip: true, wantSuccess: true},
		{name: "client-independent-trust", host: "origin.test", clientUntrusted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			certificate, roots := originCertificate(t, test.host, test.expired)
			origin := http3TestOrigin(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "verified") }))
			e, _, ca := mitmEngine(t, Config{})
			e.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"],"h3":true}`)
			e.mitm.SkipServerCertVerify = test.skip
			if test.untrusted {
				roots = x509.NewCertPool()
			}
			var clientTLS *tls.Config
			if test.clientUntrusted {
				clientTLS = &tls.Config{RootCAs: roots, ServerName: "origin.test", MinVersion: tls.VersionTLS13}
			}
			client, _ := http3TestClient(t, e, origin, ca, roots, quic.Version1, clientTLS)
			response, err := client.Get("https://origin.test/verify")
			if test.wantSuccess {
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || string(body) != "verified" {
					t.Fatal("verified HTTP/3 request failed", string(body), err)
				}
			} else if err == nil {
				_ = response.Body.Close()
				t.Fatal("HTTP/3 accepted an invalid trust chain or server identity")
			}
		})
	}
}

func TestHTTP3RequestCancellationKeepsConnectionAndServiceCloseStopsStreams(t *testing.T) {
	certificate, roots := originCertificate(t, "origin.test", false)
	cancelled := make(chan struct{}, 2)
	origin := http3TestOrigin(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	e, _, ca := mitmEngine(t, Config{})
	e.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"],"h3":true}`)
	client, dials := http3TestClient(t, e, origin, ca, roots, quic.Version1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://origin.test/stream", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, len("data: first\n\n"))
	if _, err = io.ReadFull(response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP/3 request cancellation did not cancel its upstream stream")
	}
	response, err = client.Get("https://origin.test/ok")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ok" || dials.Load() != 1 {
		t.Fatal("cancelling one HTTP/3 stream terminated the shared QUIC connection", string(body), err, dials.Load())
	}
	response, err = client.Get("https://origin.test/stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(response.Body, prefix); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { e.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("service close waited for a live QUIC stream")
	}
	_, err = response.Body.Read(make([]byte, 1))
	_ = response.Body.Close()
	if err == nil {
		t.Fatal("service close left the HTTP/3 response readable")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("service close left an upstream HTTP/3 stream alive")
	}
}
