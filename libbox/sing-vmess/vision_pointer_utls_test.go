//go:build with_utls

package vless

import (
	"net"
	"runtime"
	"testing"

	"github.com/sagernet/sing/common/logger"

	utls "github.com/metacubex/utls"
)

func TestBboxVisionUTLS(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	tlsConn := utls.UClient(left, &utls.Config{ServerName: "vision.test"}, utls.HelloChrome_Auto)
	vision, err := NewVisionConn(tlsConn, tlsConn, [16]byte{}, logger.NOP())
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if vision.input.Len() != 0 || vision.rawInput.Len() != 0 || vision.netConn != left {
		t.Fatal("Vision did not retain the uTLS buffers and underlying transport")
	}
}
