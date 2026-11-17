//go:build with_utls

package libbox

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func TestUINodeSubscriptionMixedBase64ProtocolAndTransportBatch(t *testing.T) {
	// A public mixed subscription exposed the whole-batch rejection. Recreate
	// its protocol/transport distribution with documentation hosts and test
	// keys, so the regression never stores real servers or access credentials.
	realityKey := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	links := make([]string, 0, 46)
	for index := range 22 {
		options := "type=tcp&security=none"
		switch {
		case index < 9:
			options = "type=tcp&security=reality&flow=xtls-rprx-vision&fp=chrome&sni=www.example.org&pbk=" + realityKey + "&sid=0123abcd"
		case index < 14:
			options = "type=grpc&mode=gun&serviceName=service&security=reality&fp=chrome&sni=www.example.org&pbk=" + realityKey + "&sid=0123abcd"
		case index < 19:
			options = "type=ws&path=%2Fws&host=cdn.example.org&security=tls&sni=www.example.org"
		case index < 21:
			options = "type=tcp&security=tls&sni=www.example.org"
		}
		links = append(links, "vless://"+uiNodeTestUUID+"@vless.example.com:443?encryption=none&"+options+"#VLESS-"+strconv.Itoa(index))
	}
	for index := range 7 {
		links = append(links, "vless://"+uiNodeTestUUID+"@xhttp.example.com:443?type=xhttp&security=tls&sni=www.example.org#XHTTP-"+strconv.Itoa(index))
	}
	for index := range 15 {
		links = append(links, "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@ss.example.com:8388#SS-"+strconv.Itoa(index))
	}
	vmess := `{"v":"2","ps":"VMess","add":"vmess.example.com","port":80,"id":"` + uiNodeTestUUID + `","aid":0,"scy":"auto","net":"tcp","type":"http","host":"www.example.org","path":"/one","tls":""}`
	links = append(links, "vmess://"+base64.StdEncoding.EncodeToString([]byte(vmess)),
		"trojan://password@trojan.example.com:443?type=tcp&security=tls&sni=www.example.org#Trojan")
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	updated := uiSubscriptionTestApply(t, uiNodeDefaultConfig, body, `[]`, true)
	names, err := uiNodeTextList(updated.Tags)
	if err != nil || len(names) != 46 || updated.Added != 46 || updated.Skipped != 0 ||
		updated.Warnings != `[]` {
		t.Fatalf("mixed Base64 source: names=%d, error=%v, report=%+v", len(names), err, updated)
	}
	if err := CheckConfig(updated.Config); err != nil {
		t.Fatalf("mixed supported batch failed complete core validation: %v", err)
	}
	repeated := uiSubscriptionTestApply(t, updated.Config, body, updated.Tags, true)
	if repeated.Config != updated.Config || repeated.Added != 46 || repeated.Skipped != 0 {
		t.Errorf("mixed Base64 refresh changed nodes or accumulated duplicates")
	}
}
