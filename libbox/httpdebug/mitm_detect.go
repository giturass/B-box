package httpdebug

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type tlsProtocolProbe struct {
	data  []byte
	http1 bool
}

// No ALPN does not imply a non-HTTP protocol. Read a bounded request line while
// also watching the origin, so a server-first protocol never waits for a client
// request. All inspected bytes are replayed on the same TLS connections.
func inspectTLSProtocol(ctx context.Context, client, upstream *tls.Conn) (net.Conn, net.Conn, bool, error) {
	deadline := time.Now().Add(10 * time.Second)
	if err := errors.Join(client.SetReadDeadline(deadline), upstream.SetReadDeadline(deadline)); err != nil {
		client.Close()
		upstream.Close()
		return nil, nil, false, err
	}
	clientResult, serverResult := make(chan tlsProtocolProbe, 1), make(chan tlsProtocolProbe, 1)
	go func() { clientResult <- probeHTTPRequestLine(client) }()
	go func() {
		var first [1]byte
		n, _ := upstream.Read(first[:])
		serverResult <- tlsProtocolProbe{data: first[:n]}
	}()
	var fromClient, fromServer tlsProtocolProbe
	var clientDone, serverDone bool
	select {
	case fromClient = <-clientResult:
		clientDone = true
	case fromServer = <-serverResult:
		serverDone = true
	case <-ctx.Done():
		client.Close()
		upstream.Close()
	}
	// Go crypto/tls retains partial records across temporary read timeouts.
	// Join both probes before clearing their deadlines or handing off a reader.
	err := errors.Join(client.SetReadDeadline(time.Now()), upstream.SetReadDeadline(time.Now()))
	if err != nil {
		client.Close()
		upstream.Close()
	}
	if !clientDone {
		fromClient = <-clientResult
	}
	if !serverDone {
		fromServer = <-serverResult
	}
	err = errors.Join(err, client.SetReadDeadline(time.Time{}), upstream.SetReadDeadline(time.Time{}))
	if ctx.Err() != nil {
		client.Close()
		upstream.Close()
		return nil, nil, false, ctx.Err()
	}
	if err != nil {
		client.Close()
		upstream.Close()
		return nil, nil, false, err
	}
	return replayTLS(client, fromClient.data), replayTLS(upstream, fromServer.data),
		fromClient.http1 && len(fromServer.data) == 0, nil
}

func probeHTTPRequestLine(conn net.Conn) tlsProtocolProbe {
	reader := bufio.NewReader(conn)
	result := tlsProtocolProbe{}
	method := true
	for len(result.data) < MaxHeaderBytes {
		value, err := reader.ReadByte()
		if err != nil {
			break
		}
		result.data = append(result.data, value)
		if value == '\n' {
			line := strings.TrimSuffix(strings.TrimSuffix(string(result.data), "\n"), "\r")
			verb, rest, found := strings.Cut(line, " ")
			target, version, complete := strings.Cut(rest, " ")
			major, minor, valid := http.ParseHTTPVersion(version)
			result.http1 = found && complete && verb != "" && target != "" && valid && major == 1 && (minor == 0 || minor == 1)
			break
		}
		if method {
			if value == ' ' && len(result.data) > 1 {
				method = false
			} else if !httpTokenByte(value) {
				break
			}
		} else if value < ' ' && value != '\r' || value == 127 {
			break
		}
	}
	// Discard a probe's timeout, but retain any prefetched plaintext. The next
	// reader resumes on tls.Conn itself, including its real EOF/error state.
	buffered, _ := reader.Peek(reader.Buffered())
	result.data = append(result.data, buffered...)
	return result
}

func httpTokenByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' ||
		strings.ContainsRune("!#$%&'*+-.^_`|~", rune(value))
}

type replayTLSConn struct {
	*tls.Conn // retain CloseWrite for raw TLS streams
	reader    io.Reader
}

func (c *replayTLSConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func replayTLS(conn *tls.Conn, data []byte) net.Conn {
	if len(data) == 0 {
		return conn
	}
	return &replayTLSConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(data), conn)}
}
