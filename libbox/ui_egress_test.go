package libbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUIEgressSourceParsers(t *testing.T) {
	fixtures := []struct {
		url, body string
	}{
		{"https://www.cloudflare.com/cdn-cgi/trace", "fl=123\r\nip=203.0.113.9\r\nloc=GB\r\n"},
		{"https://api.ip.sb/geoip", "{\"ip\":\"203.0.113.9\",\"country_code\":\"GB\"}"},
		{"https://ipinfo.io/json", "{\"ip\":\"203.0.113.9\",\"country\":\"GB\"}"},
		{"https://ipwho.is", "{\"ip\":\"203.0.113.9\",\"country_code\":\"GB\"}"},
		{"https://get.geojs.io/v1/ip/geo.json", "{\"ip\":\"203.0.113.9\",\"country_code\":\"GB\"}"},
		{"https://api.country.is", "{\"ip\":\"203.0.113.9\",\"country\":\"GB\"}"},
		{"https://api.ipquery.io/?format=json", "{\"ip\":\"203.0.113.9\",\"location\":{\"country_code\":\"GB\"}}"},
		{"https://ident.me/json", "{\"ip\":\"203.0.113.9\",\"cc\":\"GB\"}"},
	}
	if len(uiIPSources) != len(fixtures) {
		t.Fatal("IP source count differs from FlClash")
	}
	for index, fixture := range fixtures {
		if uiIPSources[index].URL != fixture.url {
			t.Fatalf("IP source order differs at %d", index)
		}
		info, err := parseUIOutboundIP(uiIPSources[index], fixture.body)
		if err != nil || info["ip"] != "203.0.113.9" || info["country"] != "GB" {
			t.Fatalf("%s: info=%v err=%v", fixture.url, info, err)
		}
	}
	info, err := parseUIOutboundIP(uiIPSources[0], "ip=2001:db8::9\nloc=JP")
	if err != nil || info["ip"] != "2001:db8::9" {
		t.Fatalf("IPv6 trace: %v %v", info, err)
	}
	for _, fixture := range []struct {
		source uiIPSource
		body   string
	}{
		{uiIPSources[0], "ip=203.0.113.9\rloc="},
		{uiIPSources[2], "{\"ip\":\"203.0.113.9\",\"country\":\"\"}"},
	} {
		info, err := parseUIOutboundIP(fixture.source, fixture.body)
		if err != nil || info["ip"] != "203.0.113.9" || info["country"] != "" {
			t.Fatalf("an unknown country must preserve the public IP as in FlClash: %v %v", info, err)
		}
	}
	for _, body := range []string{
		"captcha", "{}", "{\"ip\":1,\"country\":\"GB\"}",
		"{\"ip\":\"203.0.113.9\"}", "{\"ip\":\"10.0.0.1\",\"country\":\"GB\"}",
	} {
		if _, err := parseUIOutboundIP(uiIPSources[2], body); err == nil {
			t.Fatalf("accepted invalid IP response: %s", body)
		}
	}
}

func TestUIEgressPublicIP(t *testing.T) {
	for _, ip := range []string{"203.0.113.9", "8.8.8.8", "2001:db8::9", "::ffff:8.8.8.8"} {
		if !uiPublicIP(ip) || !mentionsUIPublicIP("ip="+ip+"\nloc=GB") {
			t.Fatalf("public address rejected: %s", ip)
		}
	}
	for _, ip := range []string{
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1",
		"100.64.0.1", "100.127.255.254", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fe80::1", "ff02::1", "::ffff:192.168.1.1", "not-an-ip",
	} {
		if uiPublicIP(ip) || mentionsUIPublicIP("ip="+ip) {
			t.Fatalf("nonpublic address accepted: %s", ip)
		}
	}
	if mentionsUIPublicIP("<html>Captcha: verify you are human at www.cloudflare.com</html>") {
		t.Fatal("captcha without a public IP must not win the race")
	}
}

func TestUIEgressRaceConcurrencyAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan uiIPSource, len(uiIPSources))
	cancelled := make(chan struct{}, len(uiIPSources))
	release := make(chan struct{})
	var active, maximum atomic.Int32
	type result struct {
		source uiIPSource
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		source, _, err := lookupUIOutboundIP(ctx, uiIPSources, 10*time.Millisecond, uiIPConcurrency,
			func(ctx context.Context, source uiIPSource) (*UIHTTPResponse, error) {
				count := active.Add(1)
				defer active.Add(-1)
				for old := maximum.Load(); count > old; old = maximum.Load() {
					if maximum.CompareAndSwap(old, count) {
						break
					}
				}
				started <- source
				if source == uiIPSources[2] {
					select {
					case <-release:
						return &UIHTTPResponse{Status: 200, Content: []byte("ip=203.0.113.9")}, nil
					case <-ctx.Done():
					}
				} else {
					<-ctx.Done()
				}
				cancelled <- struct{}{}
				return nil, ctx.Err()
			})
		completed <- result{source, err}
	}()
	for index := 0; index < uiIPConcurrency; index++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("staggered race did not start three sources")
		}
	}
	select {
	case <-started:
		t.Fatal("race exceeded its three-request concurrency limit")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	answer := <-completed
	if answer.err != nil || answer.source != uiIPSources[2] || maximum.Load() != 3 {
		t.Fatalf("unexpected race result: %v max=%d", answer, maximum.Load())
	}
	for index := 0; index < 2; index++ {
		select {
		case <-cancelled:
		case <-ctx.Done():
			t.Fatal("winner did not cancel a losing request")
		}
	}
}

func TestUIEgressRaceAdvancesAfterFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var calls int
	source, _, err := lookupUIOutboundIP(ctx, uiIPSources, time.Hour, 3,
		func(ctx context.Context, source uiIPSource) (*UIHTTPResponse, error) {
			calls++
			switch calls {
			case 1:
				return &UIHTTPResponse{Status: 200, Content: []byte("Captcha 127.0.0.1")}, nil
			case 2:
				return &UIHTTPResponse{Status: 429, Content: []byte("203.0.113.9")}, nil
			default:
				return &UIHTTPResponse{Status: 200, Content: []byte("ip=203.0.113.9")}, nil
			}
		})
	if err != nil || source != uiIPSources[2] || calls != 3 {
		t.Fatalf("failed sources must immediately release their slot: %v %v %d", source, err, calls)
	}
}

func uiTestRoutedProbe(t *testing.T, current func() bool) *UIEgressProbe {
	probe := newUIEgressProbe(current, func(ctx context.Context, conn net.Conn, address string, onClose func(error)) {
		peer, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err != nil {
			onClose(err)
			return
		}
		defer peer.Close()
		done := make(chan struct{})
		go func() { io.Copy(peer, conn); peer.Close(); close(done) }()
		_, err = io.Copy(conn, peer)
		conn.Close()
		<-done
		onClose(err)
	})
	t.Cleanup(probe.Close)
	return probe
}

func TestUIEgressGetStatusLimitsAndServiceChange(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	var requests atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("User-Agent") != uiProbeUserAgent {
			t.Error("probe user agent differs from FlClash")
		}
		switch r.URL.Path {
		case "/limited":
			w.WriteHeader(429)
			io.WriteString(w, "quota exceeded")
		case "/large":
			io.WriteString(w, strings.Repeat("x", uiQualityBodyLimit+1))
		case "/trace-limit":
			io.WriteString(w, strings.Repeat("x", uiIPBodyLimit+1))
		case "/changed":
			close(entered)
			<-release
			io.WriteString(w, "203.0.113.9")
		}
	}))
	defer server.Close()
	probe := uiTestRoutedProbe(t, current.Load)
	response, err := probe.Get(server.URL+"/limited", 1000)
	if err != nil || response.Status != 429 || string(response.Content) != "quota exceeded" {
		t.Fatalf("quality lookup must retain status and body: %v %v", response, err)
	}
	if _, err := probe.Get(server.URL+"/large", 1000); err == nil {
		t.Fatal("oversized quality response accepted")
	}
	if _, err := probe.get(context.Background(), server.URL+"/trace-limit", uiIPBodyLimit); err == nil {
		t.Fatal("oversized IP response accepted")
	}
	completed := make(chan error, 1)
	go func() { _, err := probe.Get(server.URL+"/changed", 1000); completed <- err }()
	<-entered
	current.Store(false)
	close(release)
	if err := <-completed; err == nil || !strings.Contains(err.Error(), "service changed") {
		t.Fatalf("stale response must be discarded: %v", err)
	}
	count := requests.Load()
	if _, err := probe.Get(server.URL, 1000); err == nil || requests.Load() != count {
		t.Fatal("changed service must not start a new probe")
	}
}

func TestUIEgressDeadlineAndCloseCancelRoutedDial(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual=%t", manual), func(t *testing.T) {
			entered := make(chan context.Context, 1)
			stopped := make(chan struct{})
			probe := newUIEgressProbe(func() bool { return true },
				func(ctx context.Context, conn net.Conn, address string, onClose func(error)) {
					entered <- ctx
					<-ctx.Done()
					onClose(ctx.Err())
					close(stopped)
				})
			defer probe.Close()
			completed := make(chan error, 1)
			timeout := int64(40)
			if manual {
				timeout = 8000
			}
			go func() { _, err := probe.Get("http://probe.invalid", timeout); completed <- err }()
			ctx := <-entered
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("router lost the request deadline when HTTP detached its dial context")
			}
			want := context.DeadlineExceeded
			if manual {
				probe.Close()
				want = context.Canceled
			}
			select {
			case err := <-completed:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt the HTTP request")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("routed dial was left running")
			}
		})
	}
}

func TestUIEgressNeverFallsBackToDirect(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer server.Close()
	probe := newUIEgressProbe(func() bool { return true },
		func(ctx context.Context, conn net.Conn, address string, onClose func(error)) { onClose(errors.New("route rejected")) })
	defer probe.Close()
	if _, err := probe.Get(server.URL, 1000); err == nil || reached.Load() {
		t.Fatal("failed routing must not use the device's direct network")
	}
}

func TestUIEgressDirectDeadlineAndClose(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual=%t", manual), func(t *testing.T) {
			entered, stopped := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				close(entered)
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			defer server.CloseClientConnections()
			probe := NewUIDirectEgressProbe()
			defer probe.Close()
			completed := make(chan error, 1)
			timeout := int64(1000)
			if manual {
				timeout = 8000
			}
			go func() { _, err := probe.Get(server.URL, timeout); completed <- err }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("stopped-service probe did not reach the device network")
			}
			want := context.DeadlineExceeded
			if manual {
				probe.Close()
				want = context.Canceled
			}
			select {
			case err := <-completed:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("direct request was not cancelled")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("direct connection was left open")
			}
			probe.Close()
			if _, err := probe.Get(server.URL, 1000); !errors.Is(err, context.Canceled) || requests.Load() != 1 {
				t.Fatalf("a closed direct session must not start another request: %v", err)
			}
		})
	}
}

func TestUIEgressIPWinnerKeepsQualitySessionAlive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != uiProbeUserAgent {
			t.Error("probe user agent differs from FlClash")
		}
		if r.URL.Path == "/trace" {
			io.WriteString(w, "ip=203.0.113.9\nloc=GB\n")
		} else {
			io.WriteString(w, "{\"type\":\"isp\"}")
		}
	}))
	defer server.Close()
	previous := uiIPSources
	uiIPSources = []uiIPSource{{server.URL + "/trace", "loc"}}
	defer func() { uiIPSources = previous }()
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			var probe *UIEgressProbe
			if direct {
				probe = NewUIDirectEgressProbe()
				t.Cleanup(probe.Close)
			} else {
				probe = uiTestRoutedProbe(t, func() bool { return true })
			}
			encoded, err := probe.LookupOutboundIP()
			if err != nil {
				t.Fatal(err)
			}
			var info map[string]any
			if err := json.Unmarshal([]byte(encoded), &info); err != nil || info["ip"] != "203.0.113.9" ||
				info["country"] != "GB" || info["ipSource"] != "127.0.0.1" || info["checkedAt"].(float64) <= 0 {
				t.Fatalf("invalid IP result metadata: %v %v", info, err)
			}
			if response, err := probe.Get(server.URL+"/quality", 1000); err != nil || string(response.Content) != "{\"type\":\"isp\"}" {
				t.Fatalf("canceling the IP race must preserve its parent quality session: %v", err)
			}
		})
	}
}
