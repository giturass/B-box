package vless

import (
	"bytes"
	"crypto/tls"
	"net"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/sagernet/sing/common/logger"
)

func TestBboxVisionStandardTLS(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	tlsConn := tls.Client(left, &tls.Config{ServerName: "vision.test"})
	vision, err := NewVisionConn(tlsConn, tlsConn, [16]byte{}, logger.NOP())
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if vision.input.Len() != 0 || vision.rawInput.Len() != 0 || vision.netConn != left {
		t.Fatal("Vision did not retain the standard TLS buffers and underlying transport")
	}
}

func TestBboxVisionRejectsInvalidBufferLayout(t *testing.T) {
	type validBuffers struct {
		input    bytes.Reader
		rawInput bytes.Buffer
	}
	type missingInput struct {
		rawInput bytes.Buffer
	}
	type wrongInputType struct {
		input    int
		rawInput bytes.Buffer
	}
	type wrongRawInputType struct {
		input    bytes.Reader
		rawInput []byte
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	for _, test := range []struct {
		name    string
		layout  reflect.Type
		pointer unsafe.Pointer
	}{
		{"nil-pointer", reflect.TypeOf(validBuffers{}), nil},
		{"nil-type", nil, unsafe.Pointer(new(validBuffers))},
		{"not-struct", reflect.TypeOf(0), unsafe.Pointer(new(int))},
		{"missing-input", reflect.TypeOf(missingInput{}), unsafe.Pointer(new(missingInput))},
		{"wrong-input", reflect.TypeOf(wrongInputType{}), unsafe.Pointer(new(wrongInputType))},
		{"wrong-raw-input", reflect.TypeOf(wrongRawInputType{}), unsafe.Pointer(new(wrongRawInputType))},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := tlsRegistry
			defer func() { tlsRegistry = original }()
			tlsRegistry = []func(net.Conn) (bool, net.Conn, reflect.Type, unsafe.Pointer){
				func(net.Conn) (bool, net.Conn, reflect.Type, unsafe.Pointer) {
					return true, left, test.layout, test.pointer
				},
			}
			_, err := NewVisionConn(left, left, [16]byte{}, logger.NOP())
			if err == nil || !strings.Contains(err.Error(), "layout") {
				t.Fatal("invalid registered TLS layout was accepted", err)
			}
		})
	}
}
