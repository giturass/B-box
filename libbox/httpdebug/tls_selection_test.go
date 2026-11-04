package httpdebug

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func occupyConnections(t *testing.T, e *Engine, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		peer, inbound := net.Pipe()
		t.Cleanup(func() { peer.Close(); inbound.Close() })
		if !e.Handle(context.Background(), inbound, Metadata{Host: "example.test", Protocol: "http", Port: 80}, nil, nil, nil) {
			t.Fatalf("connection %d was not admitted", i)
		}
	}
}

func TestMoreThan32ConnectionsRemainIntercepted(t *testing.T) {
	e, _, ca := mitmEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[{"pattern":".","data":"intercepted"}]}`).RewriteProgram
	occupyConnections(t, e, 48)
	raw, inbound, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer inbound.Close()
	raw.SetDeadline(time.Now().Add(3 * time.Second))
	client := tls.Client(raw, &tls.Config{ServerName: "origin.test", RootCAs: ca, NextProtos: []string{"http/1.1"}})
	handshake := make(chan error, 1)
	go func() { handshake <- client.Handshake() }()
	conn, metadata, selected := e.SelectTLS(context.Background(), inbound, Metadata{Host: "old-mapping.test", Port: 443, Protocol: "tls"})
	if !selected || e.captureState().Connections != 49 {
		t.Fatal("selected ClientHello was not tracked")
	}
	defer conn.Close()
	if !e.Handle(context.Background(), conn, metadata, nil, nil, nil) {
		t.Fatal("concurrent connections prevented TLS processing")
	}
	if err = <-handshake; err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(client, "GET / HTTP/1.1\r\nHost: origin.test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "intercepted" || e.captureState().Connections != 49 {
		t.Fatalf("selected flow lost its processing: %q %v", body, err)
	}
	plain := debugClient(t, e, "127.0.0.1:1")
	response, err = plain.Get("http://example.test/above-32")
	if err != nil {
		t.Fatal("HTTP above 32 connections was not processed:", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "intercepted" {
		t.Fatalf("HTTP above 32 connections bypassed processing: %q %v", body, err)
	}
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("service shutdown blocked after TLS selection was handed to the engine")
	}
	if e.captureState().Connections != 0 {
		t.Fatal("service shutdown retained intercepted connections")
	}
}

func TestAbandonedTLSSelectionEndsOnCloseCancelAndStop(t *testing.T) {
	for _, boundary := range []string{"close", "cancel", "service"} {
		t.Run(boundary, func(t *testing.T) {
			e, _, ca := mitmEngine(t, Config{})
			raw, inbound, err := mitmTCPPair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			defer inbound.Close()
			raw.SetDeadline(time.Now().Add(2 * time.Second))
			client := tls.Client(raw, &tls.Config{ServerName: "origin.test", RootCAs: ca})
			done := make(chan error, 1)
			go func() { done <- client.Handshake() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, _, selected := e.SelectTLS(ctx, inbound, Metadata{Host: "origin.test", Port: 443, Protocol: "tls"})
			if !selected {
				t.Fatal("TLS was not selected")
			}
			closed := make(chan struct{})
			go func() {
				switch boundary {
				case "close":
					conn.Close()
				case "cancel":
					cancel()
				}
				e.Close()
				close(closed)
			}()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("abandoned selection prevented service shutdown")
			}
			if e.captureState().Connections != 0 {
				t.Fatal("TLS selection leaked")
			}
			if err = <-done; err == nil {
				t.Fatal("abandoned TLS handshake unexpectedly completed")
			}
		})
	}
}
