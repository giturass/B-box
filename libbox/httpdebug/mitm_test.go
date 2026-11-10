package httpdebug

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mitmEngine(t *testing.T, config Config) (*Engine, *Store, *x509.CertPool) {
	t.Helper()
	e, store := testEngine(t, config)
	e.mitm = &MITMConfig{Enabled: true, Hosts: []string{"origin.test"}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := GenerateCA(key)
	if err != nil {
		t.Fatal(err)
	}
	e.ca, err = loadAuthority(pem, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		t.Fatal("invalid generated CA")
	}
	return e, store, pool
}

// The origin certificate is constructed independently of the MITM issuer.
func originCertificate(t *testing.T, host string, expired bool) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "independent origin root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		leaf.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, issuer, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(issuer)
	return tls.Certificate{Certificate: [][]byte{der, rootDER}, PrivateKey: key, Leaf: parsed}, pool
}

func mitmClient(t *testing.T, e *Engine, origin string, ca, roots *x509.CertPool, h2 bool) (*http.Client, *atomic.Int32) {
	t.Helper()
	dials := &atomic.Int32{}
	var connections atomic.Int32
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: h2,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			client, inbound, err := mitmTCPPair(ctx)
			if err != nil {
				return nil, err
			}
			if !e.Handle(context.Background(), inbound, Metadata{ConnectionID: fmt.Sprintf("mitm-client-%d", connections.Add(1)), Host: "origin.test", Port: 443, Protocol: "tls",
				Outbound: "selected-route", Rule: "selected-rule", UID: 1234, Packages: []string{"com.test"}},
				func(ctx context.Context) (net.Conn, error) {
					dials.Add(1)
					return (&net.Dialer{}).DialContext(ctx, "tcp", origin)
				}, roots, nil) {
				client.Close()
				inbound.Close()
				return nil, errors.New("MITM did not accept the test connection")
			}
			return client, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}, dials
}

// TLS 1.3 certificate rejection can write an alert while the peer is still
// writing its handshake flight. Real TCP is duplex and buffered; net.Pipe's
// synchronous writes would deadlock this fixture until a timeout.
func mitmTCPPair(ctx context.Context) (net.Conn, net.Conn, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	defer listener.Close()
	client, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		return nil, nil, err
	}
	inbound, err := listener.Accept()
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, inbound, nil
}

func TestMITMHTTP1AndHTTP2UseVerifiedConnectionAndPreserveMessages(t *testing.T) {
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		for _, h2 := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-http2-%v", version, h2), func(t *testing.T) {
				cert, roots := originCertificate(t, "origin.test", false)
				origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Host != "origin.test" || r.URL.RawQuery != "q=1;raw=2&esc=%2F" {
						t.Error("request target changed")
					}
					if (r.ProtoMajor == 2) != h2 {
						t.Errorf("upstream HTTP version: %s", r.Proto)
					}
					if r.Header.Get("X-Forwarded-For") != "original" {
						t.Error("forwarding header changed")
					}
					body, _ := io.ReadAll(r.Body)
					if r.Trailer.Get("X-Checksum") != "complete" {
						t.Error("request trailer lost")
					}
					w.Header().Add("Set-Cookie", "a=1")
					w.Header().Add("Set-Cookie", "a=2")
					w.Header().Add("Trailer", "Grpc-Status")
					w.WriteHeader(201)
					w.Write(body)
					w.Header().Set("Grpc-Status", "0")
				}))
				origin.EnableHTTP2 = h2
				origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: version, MaxVersion: version}
				origin.StartTLS()
				defer origin.Close()
				e, store, ca := mitmEngine(t, Config{BodyLimit: 64})
				e.mitm.H2 = h2
				client, dials := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, h2)
				for i := 0; i < 2; i++ {
					request, _ := http.NewRequest("POST", "https://origin.test/echo?q=1;raw=2&esc=%2F", strings.NewReader(strings.Repeat("payload", 100)))
					request.Header.Set("X-Forwarded-For", "original")
					request.ContentLength = -1
					request.Trailer = http.Header{"X-Checksum": []string{"complete"}}
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil || len(body) != 700 || response.StatusCode != 201 || (response.ProtoMajor == 2) != h2 {
						t.Fatalf("response: %+v, %d, %v", response, len(body), err)
					}
					if response.Trailer.Get("Grpc-Status") != "0" {
						t.Fatal("response trailer lost")
					}
				}
				if dials.Load() != 1 {
					t.Fatalf("verified connection was discarded/replaced: %d dials", dials.Load())
				}
				items := waitComplete(t, store, 2)
				if items[0].ID == items[1].ID {
					t.Fatal("HTTPS requests on one connection shared an exchange ID")
				}
				for _, item := range items {
					detail, err := store.Detail(item.ID)
					if err != nil {
						t.Fatal(err)
					}
					if detail.Metadata.ConnectionID != "mitm-client-1" || detail.Metadata.CaptureSource != "MITM" || detail.Metadata.Outbound != "selected-route" || detail.Metadata.UID != 1234 {
						t.Fatal("original route or owner lost")
					}
					if detail.Metadata.TLS == nil || detail.Metadata.TLS.UpstreamVersion != tlsVersion(version) || len(detail.Metadata.TLS.UpstreamCertificate) != 64 {
						t.Fatal("missing negotiated TLS metadata")
					}
					if len(detail.RequestBody) != 64 || len(detail.ResponseBody) != 64 || !detail.Response.BodyTruncated || !detail.Response.BodyComplete {
						t.Fatal("stream limit or completion lost")
					}
					if len(detail.Response.Trailers) != 1 || detail.Response.Trailers[0].Value != "0" {
						t.Fatal("trailer not captured")
					}
					if len(detail.Request.Trailers) != 1 || detail.Request.Trailers[0].Value != "complete" {
						t.Fatal("request trailer not captured")
					}
					count := 0
					for _, h := range detail.Response.Headers {
						if strings.EqualFold(h.Name, "Set-Cookie") {
							count++
						}
					}
					if count != 2 {
						t.Fatal("duplicate cookies lost")
					}
				}
			})
		}
	}
}

func TestMITMRejectsUntrustedExpiredAndWrongNameBeforePresentingCA(t *testing.T) {
	for _, kind := range []string{"untrusted", "expired", "wrong-name"} {
		t.Run(kind, func(t *testing.T) {
			host := "origin.test"
			if kind == "wrong-name" {
				host = "different.test"
			}
			cert, roots := originCertificate(t, host, kind == "expired")
			if kind == "untrusted" {
				roots = x509.NewCertPool()
			}
			var requests atomic.Int32
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			origin.StartTLS()
			defer origin.Close()
			e, store, ca := mitmEngine(t, Config{})
			client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, false)
			var sawCertificate atomic.Bool
			client.Transport.(*http.Transport).TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error { sawCertificate.Store(true); return nil }
			if response, err := client.Get("https://origin.test/"); err == nil {
				response.Body.Close()
				t.Fatal("invalid upstream accepted")
			}
			if sawCertificate.Load() || requests.Load() != 0 {
				t.Fatal("unverified upstream received traffic or a trusted local certificate was presented")
			}
			item := waitComplete(t, store, 1)[0]
			if !strings.Contains(item.Error, "Upstream TLS") || !item.DomainOnly || item.Method != "TLS" {
				t.Fatalf("incorrect TLS failure record: %+v", item)
			}
			if e.tlsFailed.Load() != 1 || e.plaintextConnections.Load() != 0 {
				t.Fatal("handshake failure reported as decrypted")
			}
		})
	}
}

func TestMITMClientPinningFailureClosesVerifiedUpstream(t *testing.T) {
	cert, roots := originCertificate(t, "origin.test", false)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("pinned client sent HTTP") }))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.StartTLS()
	defer origin.Close()
	e, store, ca := mitmEngine(t, Config{})
	e.mitm.SkipServerCertVerify = true
	client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, false)
	client.Transport.(*http.Transport).TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error { return errors.New("certificate pin mismatch") }
	if _, err := client.Get("https://origin.test/"); err == nil || !strings.Contains(err.Error(), "certificate pin mismatch") {
		t.Fatal("client pinning failure was bypassed or replaced by a transport timeout", err)
	}
	if item := waitComplete(t, store, 1)[0]; !strings.Contains(item.Error, "Client TLS") {
		t.Fatalf("wrong phase: %+v", item)
	}
	e.Close()
}

func TestMITMSelectionReplaysUnselectedAndNonHTTPClientHello(t *testing.T) {
	for _, kind := range []string{"empty-list", "unselected", "non-http"} {
		t.Run(kind, func(t *testing.T) {
			config := Config{}
			protos := []string{"http/1.1"}
			if kind == "non-http" {
				protos = []string{"mqtt"}
			}
			e, store, _ := mitmEngine(t, config)
			if kind == "empty-list" {
				e.mitm.Hosts = nil
			}
			if kind == "unselected" {
				e.mitm.Hosts = []string{"other.test"}
			}
			cert, roots := originCertificate(t, "origin.test", false)
			rawClient, inbound := net.Pipe()
			defer rawClient.Close()
			defer inbound.Close()
			rawClient.SetDeadline(time.Now().Add(5 * time.Second))
			client := tls.Client(rawClient, &tls.Config{ServerName: "origin.test", RootCAs: roots, NextProtos: protos})
			completed := make(chan error, 1)
			go func() { completed <- client.Handshake() }()
			replay, metadata, selected := e.SelectTLS(context.Background(), inbound, Metadata{Host: "old-dns-mapping.test", Port: 443, Protocol: "tls"})
			if selected || kind != "empty-list" && metadata.Host != "origin.test" {
				t.Fatal("TLS selection ignored actual SNI or bypass")
			}
			server := tls.Server(replay, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: protos})
			if err := server.Handshake(); err != nil {
				t.Fatal("ClientHello replay lost bytes or wrote an alert:", err)
			}
			if err := <-completed; err != nil {
				t.Fatal("passthrough client failed:", err)
			}
			if store.Page(1, "").Total != 0 || e.tlsOpened.Load() != 0 {
				t.Fatal("bypassed traffic was intercepted")
			}
		})
	}
}

func TestMITMHTTP2ConcurrentStreamsCancellationAndAuthority(t *testing.T) {
	const parallel = 24
	cert, roots := originCertificate(t, "origin.test", false)
	waiting := make(chan struct{})
	entered, release := make(chan struct{}, parallel), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/waiting" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: partial\n\n")
			w.(http.Flusher).Flush()
			close(waiting)
			<-r.Context().Done()
			return
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, r.URL.Path)
	}))
	origin.EnableHTTP2 = true
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.StartTLS()
	defer origin.Close()
	e, store, ca := mitmEngine(t, Config{})
	e.mitm.H2 = true
	client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, true)
	client.Transport.(*http.Transport).MaxConnsPerHost = 1
	client.Timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://origin.test/waiting", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil || line != "data: partial\n" {
		t.Fatal("SSE was buffered", line, err)
	}
	<-waiting
	var group sync.WaitGroup
	for i := 0; i < parallel; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			path := fmt.Sprintf("/parallel-%d", i)
			result, err := client.Get("https://origin.test" + path)
			if err != nil {
				t.Error(err)
				return
			}
			body, err := io.ReadAll(result.Body)
			result.Body.Close()
			if err != nil || string(body) != path {
				t.Error("streams mixed", string(body), err)
			}
		}(i)
	}
	for range parallel {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			unblock()
			group.Wait()
			t.Fatal("HTTP/2 streams waited for the former per-connection quota")
		}
	}
	unblock()
	group.Wait()
	cancel()
	response.Body.Close()
	request, _ = http.NewRequest("GET", "https://origin.test/private", nil)
	request.Host = "excluded.test"
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, result.Body)
	result.Body.Close()
	if result.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal("unrelated authority sent through decrypted tunnel")
	}
	if store.Page(1, "excluded.test").Total != 0 {
		t.Fatal("cross-origin request recorded")
	}
	items := waitComplete(t, store, parallel+1)
	ids := make(map[string]bool)
	for _, item := range items {
		detail, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ids[item.ID] || detail.Metadata.ConnectionID != "mitm-client-1" {
			t.Fatalf("HTTP/2 streams lost their independent IDs or shared connection: %+v", detail)
		}
		ids[item.ID] = true
		if strings.HasSuffix(item.URL, "/waiting") {
			if detail.Response.BodyComplete || !strings.Contains(string(detail.ResponseBody), "partial") {
				t.Fatal("cancelled stream lost incomplete body")
			}
		}
	}
}

func TestMITMStopCancelsAnUpstreamHandshake(t *testing.T) {
	e, _, ca := mitmEngine(t, Config{})
	clientSide, inbound := net.Pipe()
	upstream, origin := net.Pipe()
	defer origin.Close()
	defer clientSide.Close()
	entered := make(chan struct{})
	if !e.Handle(context.Background(), inbound, Metadata{Host: "origin.test", Port: 443, Protocol: "tls"},
		func(context.Context) (net.Conn, error) { close(entered); return upstream, nil }, x509.NewCertPool(), nil) {
		t.Fatal("not captured")
	}
	client := tls.Client(clientSide, &tls.Config{ServerName: "origin.test", RootCAs: ca})
	finished := make(chan error, 1)
	go func() { finished <- client.Handshake() }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream handshake not reached")
	}
	stopped := make(chan struct{})
	go func() { e.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop leaked a handshake")
	}
	if err := <-finished; err == nil {
		t.Fatal("unfinished handshake succeeded")
	}
}

func TestMITMQUICRequiresOptInNameAndApplicationMatch(t *testing.T) {
	e, _, _ := mitmEngine(t, Config{})
	e.mitm = mitmConfig(t, `{"enabled":true,"block_quic":true,"hosts":["api.example.test"],"packages":["com.test"]}`)
	for _, test := range []struct {
		host     string
		packages []string
		want     bool
	}{
		{"api.example.test", []string{"com.test"}, true}, {"pinned.example.test", []string{"com.test"}, false},
		{"", []string{"com.test"}, false}, {"198.51.100.1", []string{"com.test"}, false}, {"api.example.test", nil, false},
	} {
		if got := e.RejectQUIC(Metadata{Host: test.host, Port: 443, Packages: test.packages}); got != test.want {
			t.Fatalf("QUIC match %+v: %v", test, got)
		}
	}
	e.Close()
	if e.RejectQUIC(Metadata{Host: "api.example.test", Port: 443, Packages: []string{"com.test"}}) {
		t.Fatal("stopped engine rejected QUIC")
	}
}

func TestMITMDialDeadlineAndConnectionLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := dialUpstreamTLS(context.Background(), ctx, func(ctx context.Context) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, "origin.test", nil, nil, false)
		finished <- err
	}()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("stalled dial succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("handshake timeout did not cover the dial")
	}
	lifetime, stop := context.WithCancel(context.Background())
	entered := make(chan struct{})
	bound := bindDialLifetime(lifetime, func(ctx context.Context) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go func() { _, err := bound(context.Background()); finished <- err }()
	<-entered
	stop()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("route cancellation lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("detached HTTP dial outlived capture")
	}
}
