package libbox

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func uiWARPTestPeer(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), base64.StdEncoding.EncodeToString(der)
}
func uiWARPTestEnrollment(t *testing.T, peer, ipv4, ipv6 string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"id": "mock-device", "token": "mock-secret-api-token", "account": map[string]string{"license": "mock-secret-license"},
		"config": map[string]any{"interface": map[string]any{"addresses": map[string]string{"v4": ipv4, "v6": ipv6}}, "peers": []any{map[string]any{
			"public_key": peer, "endpoint": map[string]string{"v4": "162.159.198.1:443", "host": "162.159.198.1:443"},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func uiWARPTestRegister(t *testing.T, ctx context.Context, server *httptest.Server, probe *UIEgressProbe) (string, error) {
	t.Helper()
	task := newUIWARPRegistrationTask(probe)
	defer task.Close()
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(probe.ctx, cancel)
	defer stop()
	defer cancel()
	return uiWARPRegister(requestCtx, task.probe, server.URL, "WARP", 3)
}

func TestUIWARPTwoPhaseRegistrationAndConfigMerge(t *testing.T) {
	peerPEM, peerDER := uiWARPTestPeer(t)
	var calls atomic.Int32
	enrolledKeys := make(chan *ecdsa.PublicKey, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := calls.Add(1)
		if r.Header.Get("User-Agent") != "WARP for Android" || r.Header.Get("CF-Client-Version") != "a-6.35-4471" ||
			r.Header.Get("Content-Type") != "application/json; charset=UTF-8" {
			t.Error("incorrect registration API headers")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		der, err := base64.StdEncoding.DecodeString(body["key"])
		if err != nil {
			t.Error("invalid API public key")
			return
		}
		switch step {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/reg" || r.Header.Get("Authorization") != "" || body["key_type"] != "curve25519" ||
				body["tunnel_type"] != "wireguard" || len(body["serial_number"]) != 16 || body["tos"] == "" {
				t.Error("incorrect initial device registration")
			}
			if _, err = ecdh.X25519().NewPublicKey(der); err != nil {
				t.Error(err)
			}
			io.WriteString(w, `{"id":"mock-device","token":"mock-secret-api-token"}`)
		case 2:
			if r.Method != http.MethodPatch || r.URL.Path != "/reg/mock-device" || r.Header.Get("Authorization") != "Bearer mock-secret-api-token" ||
				body["key_type"] != "secp256r1" || body["tunnel_type"] != "masque" {
				t.Error("incorrect MASQUE key enrollment")
			}
			parsed, parseErr := x509.ParsePKIXPublicKey(der)
			if parseErr != nil {
				t.Error(parseErr)
				return
			}
			enrolled, ok := parsed.(*ecdsa.PublicKey)
			if !ok || enrolled.Curve != elliptic.P256() {
				t.Error("enrolled key must be P-256")
				return
			}
			enrolledKeys <- enrolled
			io.WriteString(w, uiWARPTestEnrollment(t, peerPEM, "172.16.0.2", "2001:db8::2"))
		default:
			t.Error("registration repeated a non-idempotent request")
		}
	}))
	defer server.Close()
	result, err := uiWARPTestRegister(t, context.Background(), server, NewUIDirectEgressProbe())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("registration must use exactly POST then PATCH")
	}
	for _, secret := range []string{"mock-device", "mock-secret-api-token", "mock-secret-license"} {
		if strings.Contains(result, secret) {
			t.Fatal("unneeded API credential crossed the binding")
		}
	}
	var node map[string]any
	if err = json.Unmarshal([]byte(result), &node); err != nil {
		t.Fatal(err)
	}
	if node["server"] != "162.159.198.2" || node["server_port"] != float64(443) || node["http_version"] != float64(3) ||
		node["peer_public_key"] != peerDER || node["tls"].(map[string]any)["server_name"] != "consumer-masque.cloudflareclient.com" {
		t.Fatal("incorrect MASQUE node metadata")
	}
	if !reflect.DeepEqual(node["address"], []any{"172.16.0.2/32", "2001:db8::2/128"}) {
		t.Fatal("API addresses were not saved as host prefixes")
	}
	privateDER, err := base64.StdEncoding.DecodeString(node["private_key"].(string))
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.ParseECPrivateKey(privateDER)
	if err != nil || !private.PublicKey.Equal(<-enrolledKeys) {
		t.Fatal("saved private key does not match the enrolled key")
	}
	current := `{"dns":{"servers":[{"type":"local","tag":"local"}]},"route":{"final":"proxy"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]}]}`
	update, err := AddUIConfigNodes(current, "["+result+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	if update.Added != 1 || update.Skipped != 0 {
		t.Fatal("registered node was not appended")
	}
	if err = CheckConfig(update.Config); err != nil {
		t.Fatal("registered node failed full config validation", err)
	}
	root, err := uiNodeDecodeObject(update.Config)
	if err != nil {
		t.Fatal(err)
	}
	if root.object("route").text("final") != "proxy" || root.object("dns").list("servers")[0].(*uiNodeObject).text("tag") != "local" {
		t.Fatal("registration replaced the selected config's routing or DNS")
	}
}

func TestUIWARPHTTPFailuresNeverRetryOrExposeResponse(t *testing.T) {
	for _, status := range []int{302, 400, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls, redirects atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", destination.URL+"/mock-secret-api-token")
				w.WriteHeader(status)
				io.WriteString(w, `{"errors":[{"message":"mock-secret-api-token mock-secret-private-key"}]}`)
			}))
			defer server.Close()
			result, err := uiWARPTestRegister(t, context.Background(), server, NewUIDirectEgressProbe())
			if err == nil || result != "" {
				t.Fatal("HTTP rejection returned a node")
			}
			if strings.Contains(err.Error(), "mock-secret") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("API error exposed credentials or URL")
			}
			if calls.Load() != 1 || redirects.Load() != 0 {
				t.Fatal("POST retried or followed a redirect")
			}
		})
	}
}

func TestUIWARPCancellationAtBothAPIPhases(t *testing.T) {
	for _, phase := range []string{"register", "enroll"} {
		t.Run(phase, func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			cancelled := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "enroll" && r.Method == http.MethodPost {
					io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
					return
				}
				// Drain the request so net/http can observe peer cancellation.
				io.Copy(io.Discard, r.Body)
				arrived <- struct{}{}
				<-r.Context().Done()
				cancelled <- struct{}{}
			}))
			defer server.Close()
			probe := NewUIDirectEgressProbe()
			defer probe.Close()
			result := make(chan error, 1)
			go func() { _, err := uiWARPTestRegister(t, context.Background(), server, probe); result <- err }()
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("API phase was not reached")
			}
			probe.Close()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled registration returned success or another error", err)
				}
			case <-time.After(time.Second):
				t.Fatal("registration cancellation did not unblock")
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("API connection remained alive after cancellation")
			}
		})
	}
}

func TestUIWARPRejectsMalformedEnrollment(t *testing.T) {
	peer, _ := uiWARPTestPeer(t)
	for _, body := range []string{
		`not json`, `{"config":{}}`, uiWARPTestEnrollment(t, peer, "", ""), uiWARPTestEnrollment(t, peer, "0.0.0.0", ""),
		uiWARPTestEnrollment(t, peer, "2001:db8::2", ""), uiWARPTestEnrollment(t, peer, "172.16.0.2/32", ""),
		uiWARPTestEnrollment(t, "malformed-peer-key", "172.16.0.2", ""), strings.Repeat(" ", uiWARPMaxResponse+1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
				return
			}
			io.WriteString(w, body)
		}))
		result, err := uiWARPTestRegister(t, context.Background(), server, NewUIDirectEgressProbe())
		server.Close()
		if err == nil || result != "" {
			t.Fatal("invalid enrollment returned a node")
		}
	}
}

func TestUIWARPServiceChangeAndTaskReuse(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		current.Store(false)
		io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
	}))
	defer server.Close()
	probe := newUIEgressProbe(current.Load, nil)
	result, err := uiWARPTestRegister(t, context.Background(), server, probe)
	if err == nil || result != "" || calls.Load() != 1 {
		t.Fatal("service change allowed enrollment or direct fallback")
	}
	for _, version := range []int32{1, 4} {
		task := NewUIWARPRegistrationTask()
		if _, err = task.Register("WARP", version); err == nil {
			t.Fatal("invalid carrier accepted")
		}
		if _, err = task.Register("WARP", 2); err == nil || !strings.Contains(err.Error(), "already used") {
			t.Fatal("task may repeat POST")
		}
	}
	task := NewUIWARPRegistrationTask()
	task.Close()
	if result, err = task.Register("WARP", 2); result != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("closed task began registration")
	}
}

type uiWARPRoundTripper func(*http.Request) (*http.Response, error)

func (f uiWARPRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type uiWARPCompleteBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *uiWARPCompleteBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b *uiWARPCompleteBody) Close() error { return nil }

func TestUIWARPCompletedEnrollmentSurvivesCancellation(t *testing.T) {
	peer, _ := uiWARPTestPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := NewUIDirectEgressProbe()
	defer probe.Close()
	probe.client.Transport = uiWARPRoundTripper(func(request *http.Request) (*http.Response, error) {
		var body io.ReadCloser = io.NopCloser(strings.NewReader(`{"id":"mock-device","token":"mock-token"}`))
		if request.Method == http.MethodPatch {
			// A full enrollment has already reached the client when cancellation
			// wins at EOF. Its credentials must reach Android's pending store.
			body = &uiWARPCompleteBody{Reader: strings.NewReader(uiWARPTestEnrollment(t, peer, "172.16.0.2", "")), cancel: cancel}
		}
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header), Request: request}, nil
	})
	task := newUIWARPRegistrationTask(probe)
	result, err := uiWARPRegister(ctx, task.probe, uiWARPAPI, "WARP", 3)
	if err != nil || result == "" || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("complete enrollment was lost at the cancellation boundary", err)
	}
	if _, err = AddUIConfigNodes(`{}`, "["+result+"]", false); err != nil {
		t.Fatal("completed pending node is invalid", err)
	}
}
