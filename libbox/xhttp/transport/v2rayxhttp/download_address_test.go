package v2rayxhttp

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

// Host and port are independent overrides. In particular, a download port
// without a download host must not silently dial the upload's port.
func TestDownloadAddressOverrides(t *testing.T) {
	uploadConfig, err := newConfig(&option.V2RayXHTTPOptions{Mode: ModeStreamUp})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		uploadHost   string
		uploadPort   uint16
		downloadHost string
		downloadPort uint16
		wantHost     string
		wantPort     uint16
	}{
		{"inherit both", "upload.example.test", 443, "", 0, "upload.example.test", 443},
		{"override host", "upload.example.test", 443, "download.example.test", 0, "download.example.test", 443},
		{"override port", "upload.example.test", 443, "", 8443, "upload.example.test", 8443},
		{"override both", "upload.example.test", 443, "download.example.test", 8443, "download.example.test", 8443},
		{"IPv4 port override", "127.0.0.1", 443, "", 8443, "127.0.0.1", 8443},
		{"IPv6 port override", "2001:db8::1", 443, "", 8443, "2001:db8::1", 8443},
		{"replace IP with host", "127.0.0.1", 443, "download.example.test", 0, "download.example.test", 443},
		{"replace host with IP", "upload.example.test", 443, "2001:db8::2", 0, "2001:db8::2", 443},
	} {
		t.Run(test.name, func(t *testing.T) {
			uploadAddr := M.ParseSocksaddrHostPort(test.uploadHost, test.uploadPort)
			options := &option.V2RayXHTTPDownloadOptions{
				ServerOptions: option.ServerOptions{
					Server:     test.downloadHost,
					ServerPort: test.downloadPort,
				},
			}
			// Endpoint construction is lazy; no dialer or test server is needed.
			download, err := newDownloadEndpoint(context.Background(), uploadConfig, options, nil, uploadAddr)
			if err != nil {
				t.Fatal(err)
			}
			if download.serverAddr.AddrString() != test.wantHost || download.serverAddr.Port != test.wantPort {
				t.Errorf("download address = %s, want %s", download.serverAddr, M.ParseSocksaddrHostPort(test.wantHost, test.wantPort))
			}
			if download.defaultHost != test.wantHost {
				t.Errorf("HTTP host = %q, want %q", download.defaultHost, test.wantHost)
			}
			wantDomain := M.ParseSocksaddrHostPort(test.wantHost, test.wantPort).IsDomain()
			if download.serverAddr.IsDomain() != wantDomain {
				t.Errorf("download address domain classification changed: %s", download.serverAddr)
			}
		})
	}
}
