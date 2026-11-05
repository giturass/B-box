package httpdebug

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real ClientHello selection, including its unknown-ALPN path.
func selectedMITMClient(t *testing.T, e *Engine, origin string, ca, roots *x509.CertPool, protos []string) (*tls.Conn, *atomic.Int32, <-chan error) {
	t.Helper()
	raw, inbound, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close(); inbound.Close() })
	finished := make(chan error, 1)
	dials := &atomic.Int32{}
	go func() {
		conn, metadata, selected := e.SelectTLS(context.Background(), inbound,
			Metadata{ConnectionID: "selected-client", Host: "wrong-mapping.test", Port: 443, Protocol: "tls", Outbound: "original-route"})
		if !selected || !e.Handle(context.Background(), conn, metadata, func(ctx context.Context) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, "tcp", origin)
		}, roots, func(err error) { finished <- err }) {
			conn.Close()
			finished <- fmt.Errorf("TLS connection was not selected: %+v", metadata)
		}
	}()
	client := tls.Client(raw, &tls.Config{RootCAs: ca, ServerName: "origin.test", NextProtos: protos})
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if err = client.Handshake(); err != nil {
		t.Fatal(err)
	}
	return client, dials, finished
}

func TestMITMNoALPNHTTPUsesRewritesScriptsAndHTTPExports(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tlsVersion(version), func(t *testing.T) {
			cert, roots := originCertificate(t, "origin.test", false)
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != "payload" || r.Method != "PROPFIND" || r.URL.RawQuery != "raw=1;2" ||
					r.Header.Get("X-Rewritten") != "yes" || r.Host != "origin.test" {
					t.Errorf("HTTP request was not processed intact: %s %s %q %v", r.Method, r.URL, body, err)
				}
				io.WriteString(w, "original")
			}))
			origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: version, MaxVersion: version}
			origin.StartTLS()
			defer origin.Close()
			e, store, ca := mitmEngine(t, Config{})
			e.mitm.TCP = true
			e.rewrite = rewriteProgram(t, `{"enabled":true,"header_rewrite":[{"pattern":".","phase":"request","operation":"add","header":"X-Rewritten","value":"yes"}],"body_rewrite":[{"pattern":".","phase":"response","regex":"original","replacement":"changed"}]}`).RewriteProgram
			e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":".","phase":"response","requires_body":true,"source":"$done({status:202,body:$response.body+'-script'});"}]}`)
			client, dials, finished := selectedMITMClient(t, e, origin.Listener.Addr().String(), ca, roots, nil)
			reader := bufio.NewReader(client)
			for i := 0; i < 2; i++ {
				connection := "keep-alive"
				if i == 1 {
					connection = "close"
				}
				// Separate TLS records split the method; headers/body may also
				// have been prefetched by the protocol probe and must be replayed.
				for _, part := range []string{"P", "ROP", "FIND /probe?raw=1;2 HTTP/1.1\r\nHost: origin.test\r\nContent-Length: 7\r\nConnection: " + connection + "\r\n\r\npayload"} {
					if _, err := io.WriteString(client, part); err != nil {
						t.Fatal(err)
					}
				}
				response, err := http.ReadResponse(reader, &http.Request{Method: "PROPFIND"})
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 202 || string(body) != "changed-script" {
					t.Fatalf("HTTP rules did not run: %d %q %v", response.StatusCode, body, err)
				}
			}
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if dials.Load() != 1 || e.parsedRequests.Load() != 2 {
				t.Fatalf("connection was redialed or requests were not parsed: dials=%d requests=%d", dials.Load(), e.parsedRequests.Load())
			}
			for _, item := range waitComplete(t, store, 2) {
				detail, err := store.Detail(item.ID)
				if err != nil || detail.Metadata.RawTCP || detail.Metadata.ConnectionID != "selected-client" ||
					detail.Metadata.Outbound != "original-route" || detail.Metadata.TLS.ALPN != "" || detail.Metadata.TLS.UpstreamALPN != "" {
					t.Fatalf("incorrect HTTP classification or TLS metadata: %+v %v", detail, err)
				}
				if _, err := store.Curl(item.ID); err != nil {
					t.Fatalf("recognized HTTP cannot be exported: %v", err)
				}
			}
			var har strings.Builder
			if err := store.WriteHAR(&har, ""); err != nil || !strings.Contains(har.String(), "PROPFIND") {
				t.Fatalf("recognized HTTP missing from HAR: %s %v", har.String(), err)
			}
		})
	}
}

func TestMITMClientFirstRawTLSReplaysBytesAndHalfCloses(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, protocol := range []string{"", "mqtt"} {
			t.Run(fmt.Sprintf("%s-%s", tlsVersion(version), protocol), func(t *testing.T) {
				payload := []byte{0, 1, 255, 0, 2, 3, 4}
				protos := []string(nil)
				if protocol != "" {
					protos = []string{protocol}
					payload = []byte("GET /still-raw HTTP/1.1\r\nHost: origin.test\r\n\r\n")
				}
				cert, roots := originCertificate(t, "origin.test", false)
				origin, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert},
					MinVersion: version, MaxVersion: version, NextProtos: protos})
				if err != nil {
					t.Fatal(err)
				}
				defer origin.Close()
				originDone := make(chan error, 1)
				go func() {
					conn, err := origin.Accept()
					if err != nil {
						originDone <- err
						return
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(5 * time.Second))
					data, err := io.ReadAll(conn)
					if err == nil && !bytes.Equal(data, payload) {
						err = fmt.Errorf("raw payload changed: %x", data)
					}
					if err == nil {
						_, err = conn.Write(data)
					}
					if err == nil {
						err = conn.(*tls.Conn).CloseWrite()
					}
					originDone <- err
				}()
				e, store, ca := mitmEngine(t, Config{})
				e.mitm.TCP = true
				e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":".","phase":"request","source":"throw Error('raw TLS reached HTTP script');"}]}`)
				client, dials, finished := selectedMITMClient(t, e, origin.Addr().String(), ca, roots, protos)
				if _, err = client.Write(payload); err != nil {
					t.Fatal(err)
				}
				if err = client.CloseWrite(); err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(client)
				if err != nil || !bytes.Equal(data, payload) {
					t.Fatalf("raw reply or half-close lost: %x %v", data, err)
				}
				if err = <-originDone; err != nil {
					t.Fatal(err)
				}
				if err = <-finished; err != nil {
					t.Fatal(err)
				}
				item := waitComplete(t, store, 1)[0]
				detail, err := store.Detail(item.ID)
				if err != nil || !detail.Metadata.RawTCP || !bytes.Equal(detail.RequestBody, payload) ||
					!bytes.Equal(detail.ResponseBody, payload) || !detail.Request.BodyComplete || !detail.Response.BodyComplete ||
					e.parsedRequests.Load() != 0 || dials.Load() != 1 {
					t.Fatalf("raw TLS was parsed, redialed or recorded incorrectly: %+v %v", detail, err)
				}
			})
		}
	}
}

func TestMITMHTTP2OptionSeparatesClientAndOriginVersions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, originH2 := range []bool{false, true} {
			for _, clientH2 := range []bool{false, true} {
				t.Run(fmt.Sprintf("enabled=%v-originH2=%v-clientH2=%v", enabled, originH2, clientH2), func(t *testing.T) {
					cert, roots := originCertificate(t, "origin.test", false)
					origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if (r.ProtoMajor == 2) != (enabled && originH2) {
							t.Errorf("unexpected upstream HTTP version: %s", r.Proto)
						}
						io.WriteString(w, "ok")
					}))
					origin.EnableHTTP2 = originH2
					origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}
					if originH2 {
						origin.TLS.NextProtos = []string{"h2", "http/1.1"}
					}
					origin.StartTLS()
					defer origin.Close()
					e, store, ca := mitmEngine(t, Config{})
					e.mitm.H2 = enabled
					client, dials := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, clientH2)
					client.Transport.(*http.Transport).TLSClientConfig.NextProtos = []string{"http/1.1"}
					response, err := client.Get("https://origin.test/protocol")
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if (response.ProtoMajor == 2) != (enabled && clientH2) || dials.Load() != 1 {
						t.Fatalf("HTTP/2 setting or connection reuse failed: %s, %d dials", response.Proto, dials.Load())
					}
					detail, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
					wantClient, wantUpstream := "http/1.1", "http/1.1"
					if enabled && clientH2 {
						wantClient = "h2"
					}
					if enabled && originH2 {
						wantUpstream = "h2"
					}
					if detail.Metadata.TLS.ALPN != wantClient || detail.Metadata.TLS.UpstreamALPN != wantUpstream {
						t.Fatalf("the two ALPN values were conflated: %+v", detail.Metadata.TLS)
					}
				})
			}
		}
	}
}

func TestMITMServerVerificationOptionForOrdinaryAndRewrittenHTTPS(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		for _, skip := range []bool{false, true} {
			for _, kind := range []string{"untrusted", "expired", "wrong-name"} {
				t.Run(fmt.Sprintf("rewrite=%v-skip=%v-%s", rewrite, skip, kind), func(t *testing.T) {
					host := "origin.test"
					if kind == "wrong-name" {
						host = "other.test"
					}
					cert, roots := originCertificate(t, host, kind == "expired")
					if kind == "untrusted" {
						roots = x509.NewCertPool()
					}
					var received atomic.Int32
					origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						received.Add(1)
						io.WriteString(w, "origin")
					}))
					origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
					origin.StartTLS()
					defer origin.Close()
					e, _, ca := mitmEngine(t, Config{})
					e.mitm.SkipServerCertVerify = skip
					if rewrite {
						e.rewrite = rewriteProgram(t, `{"enabled":true,"header_rewrite":[{"pattern":".","phase":"request","operation":"add","header":"X-Test","value":"yes"}]}`).RewriteProgram
					}
					client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, false)
					response, err := client.Get("https://origin.test/")
					if response != nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
					if skip {
						if err != nil || response.StatusCode != 200 || received.Load() != 1 {
							t.Fatalf("explicit verification opt-out was ignored: %v %+v", err, response)
						}
					} else if received.Load() != 0 || err == nil && response.StatusCode == 200 {
						t.Fatal("upstream verification was disabled by default")
					}
				})
			}
		}
	}
}

func TestMITMStopCancelsNoALPNProtocolInspection(t *testing.T) {
	cert, roots := originCertificate(t, "origin.test", false)
	origin, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	originDone := make(chan struct{})
	go func() {
		defer close(originDone)
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		io.Copy(io.Discard, conn)
	}()
	e, _, ca := mitmEngine(t, Config{})
	e.mitm.TCP = true
	_, _, finished := selectedMITMClient(t, e, origin.Addr().String(), ca, roots, nil)
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("service stop left protocol probes running")
	}
	if err := <-finished; err == nil {
		t.Fatal("cancelled protocol inspection reported success")
	}
	select {
	case <-originDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled protocol inspection retained the upstream connection")
	}
}
