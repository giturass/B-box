// CI fixture. The server and client use separate processes, and client I/O
// moves between goroutines. KeyLogWriter is deliberately left unset.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

func main() {
	serve := flag.Bool("serve", false, "run the local fixture server")
	address := flag.String("address", "127.0.0.1:0", "loopback server")
	cert := flag.String("cert", "", "fixture certificate")
	key := flag.String("key", "", "fixture private key")
	version := flag.Int("version", 13, "TLS protocol version")
	count := flag.Int("count", 3, "request count")
	flag.Parse()
	runtime.GOMAXPROCS(2)
	if *serve {
		pair, err := tls.LoadX509KeyPair(*cert, *key)
		must(err)
		listener, err := tls.Listen("tcp", *address, &tls.Config{Certificates: []tls.Certificate{pair}})
		must(err)
		fmt.Println(listener.Addr().String())
		must(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "go-tls-body\n")
		})))
		return
	}
	data, err := os.ReadFile(*cert)
	must(err)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		panic("bad fixture certificate")
	}
	protocol := uint16(tls.VersionTLS13)
	if *version == 12 {
		protocol = tls.VersionTLS12
	}
	type transaction struct {
		Request, Response []byte
		ElapsedNS         int64
	}
	var transactions []transaction
	for i := 0; i < *count; i++ {
		started := time.Now()
		config := &tls.Config{RootCAs: roots, ServerName: "capture.test", MinVersion: protocol, MaxVersion: protocol}
		var conn *tls.Conn
		if i%2 == 0 {
			conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", *address, config)
			must(err)
		} else {
			raw, err := net.DialTimeout("tcp", *address, 5*time.Second)
			must(err)
			conn = tls.Client(raw, config) // first Write performs the handshake on its own goroutine
		}
		must(conn.SetDeadline(time.Now().Add(5 * time.Second)))
		request := []byte(fmt.Sprintf("GET /go-tls-%d-%d HTTP/1.1\r\nHost: capture.test\r\nX-Padding: %s\r\nConnection: close\r\n\r\n", *version, i, strings.Repeat("g", 6000)))
		written := make(chan error, 1)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			_, err := conn.Write(request)
			written <- err
		}()
		must(<-written)
		result := make(chan transaction, 1)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			body, err := io.ReadAll(conn)
			must(err)
			result <- transaction{Request: request, Response: body}
		}()
		item := <-result
		must(conn.Close())
		item.ElapsedNS = time.Since(started).Nanoseconds()
		transactions = append(transactions, item)
	}
	must(json.NewEncoder(os.Stdout).Encode(transactions))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
