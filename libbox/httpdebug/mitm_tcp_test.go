package httpdebug

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMITMTCPPreservesBinaryStreamsServerFirstAndHalfClose(t *testing.T) {
	for _, test := range []struct {
		name       string
		offered    []string
		negotiated string
		stopRecord bool
	}{
		{"mqtt", []string{"mqtt"}, "mqtt", false},
		{"mixed", []string{"h2", "mqtt", "http/1.1"}, "mqtt", false},
		{"none", nil, "", false},
		{"stop-recording", []string{"mqtt"}, "mqtt", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert, roots := originCertificate(t, "origin.test", false)
			protos := []string(nil)
			if test.negotiated != "" {
				protos = []string{test.negotiated}
			}
			listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: protos})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			payload := bytes.Repeat([]byte{0, 255, 1, 2, 3}, 20000)
			greeting := []byte{4, 0, 6, 255}
			originResult := make(chan error, 1)
			go func() {
				raw, err := listener.Accept()
				if err != nil {
					originResult <- err
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(5 * time.Second))
				conn := raw.(*tls.Conn)
				_, err = conn.Write(greeting)
				if err == nil {
					var data []byte
					data, err = io.ReadAll(conn)
					if err == nil && !bytes.Equal(data, payload) {
						err = io.ErrUnexpectedEOF
					}
					if err == nil {
						_, err = conn.Write(data)
					}
					if err == nil {
						err = conn.CloseWrite()
					}
				}
				originResult <- err
			}()
			e, store, ca := mitmEngine(t, Config{BodyLimit: 64})
			e.mitm.TCP = true
			// Even an HTTP script matching this host cannot consume a TCP stream.
			e.scripts = scriptProgram(t, `{"http":[{"pattern":".","phase":"request","source":"throw Error('TCP entered HTTP');"}]}`)
			rawClient, inbound, err := mitmTCPPair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer rawClient.Close()
			defer inbound.Close()
			finished := make(chan error, 1)
			go func() {
				conn, metadata, selected := e.SelectTLS(context.Background(), inbound, Metadata{ConnectionID: "tcp-client", Host: "wrong-mapping.test", Port: 443, Protocol: "tls"})
				if !selected || !metadata.RawTCP {
					finished <- io.ErrUnexpectedEOF
					conn.Close()
					return
				}
				if !e.Handle(context.Background(), conn, metadata, func(ctx context.Context) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
				}, roots, func(err error) { finished <- err }) {
					finished <- io.ErrUnexpectedEOF
					conn.Close()
				}
			}()
			client := tls.Client(rawClient, &tls.Config{RootCAs: ca, ServerName: "origin.test", NextProtos: test.offered})
			client.SetDeadline(time.Now().Add(5 * time.Second))
			if err = client.Handshake(); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(greeting))
			if _, err = io.ReadFull(client, got); err != nil || !bytes.Equal(got, greeting) {
				t.Fatalf("server-first bytes: %v %v", got, err)
			}
			if test.stopRecord {
				e.stopRecording()
			}
			if _, err = client.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err = client.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			got, err = io.ReadAll(client)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("TCP bytes changed: %d %v", len(got), err)
			}
			if err = <-originResult; err != nil {
				t.Fatal(err)
			}
			if err = <-finished; err != nil {
				t.Fatal(err)
			}
			detail, err := store.detailWithBodies(waitComplete(t, store, 1)[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Metadata.ConnectionID != "tcp-client" || !detail.Metadata.RawTCP || detail.Request.Protocol != "TLS/TCP" || detail.Metadata.TLS.ALPN != test.negotiated ||
				detail.Metadata.TLS.UpstreamCertificate == "" {
				t.Fatalf("incorrect TCP record: %+v", detail)
			}
			if test.stopRecord {
				if detail.Request.BodyBytes != 0 || detail.Response.BodyBytes != int64(len(greeting)) ||
					detail.Request.BodyComplete || detail.Response.BodyComplete || !strings.Contains(detail.Note, "Recording stopped") {
					t.Fatalf("stopped TCP record was modified: %+v", detail)
				}
			} else if detail.Request.BodyBytes != int64(len(payload)) || !detail.Request.BodyComplete ||
				!detail.Response.BodyComplete || !detail.Request.BodyTruncated || len(detail.RequestBody) != 64 {
				t.Fatalf("incorrect TCP body record: %+v", detail)
			}
			if e.parsedRequests.Load() != 0 {
				t.Fatal("TCP stream entered HTTP parser")
			}
			if _, err = store.Curl(detail.ID); err == nil {
				t.Fatal("TCP stream exported as HTTP cURL")
			}
			var har strings.Builder
			if err = store.WriteHAR(&har, ""); err != nil || !strings.Contains(har.String(), `"entries":[]`) {
				t.Fatalf("TCP stream leaked into HAR: %s %v", har.String(), err)
			}
		})
	}
}
