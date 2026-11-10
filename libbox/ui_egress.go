package libbox

// Adapted from FlClash v0.9.2 (7fd417fb9a9a277c6a9339a8ac87f907df7c25f7):
// core/outbound_ip.go and lib/common/service_probe.dart (GPL-3.0).
// Both device-network and sing-box-routed probes use the Android TLS store.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

const uiIPStagger = 800 * time.Millisecond
const uiProbeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

type uiIPSource struct {
	URL        string
	CountryKey string
}

var uiIPSources = []uiIPSource{
	{"https://www.cloudflare.com/cdn-cgi/trace", "loc"},
	{"https://api.ip.sb/geoip", "country_code"},
	{"https://ipinfo.io/json", "country"},
	{"https://ipwho.is", "country_code"},
	{"https://get.geojs.io/v1/ip/geo.json", "country_code"},
	{"https://api.country.is", "country"},
	{"https://api.ipquery.io/?format=json", "location.country_code"},
	{"https://ident.me/json", "cc"},
}

// UIEgressProbe owns every request for one dashboard refresh. Closing it
// cancels in-flight requests, including a router dial waiting for its peer.
type UIEgressProbe struct {
	ctx         context.Context
	cancel      context.CancelFunc
	client      *http.Client
	closeClient func()
	current     func() bool
	onClose     func()
	routes      *uiRouteLifecycle
	once        sync.Once
}

type uiEgressRequestContextKey struct{}

// Creation and service teardown hold uiCaptureLifecycle. Individual callers
// can close their own probe independently, so membership has its own lock.
type uiEgressProbeSet struct {
	access sync.Mutex
	probes map[*UIEgressProbe]struct{}
}

func (s *uiEgressProbeSet) add(probe *UIEgressProbe) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.probes == nil {
		s.probes = make(map[*UIEgressProbe]struct{})
	}
	s.probes[probe] = struct{}{}
	probe.onClose = func() {
		s.access.Lock()
		delete(s.probes, probe)
		s.access.Unlock()
	}
}

func (s *uiEgressProbeSet) close() {
	s.access.Lock()
	probes := make([]*UIEgressProbe, 0, len(s.probes))
	for probe := range s.probes {
		probes = append(probes, probe)
	}
	s.access.Unlock()
	for _, probe := range probes {
		probe.Close()
	}
	for _, probe := range probes {
		// DNS/dial setup must finish before its service instance is closed.
		probe.routes.wait()
	}
}

// NewUIDirectEgressProbe checks the device's current network without starting
// sing-box. The Android host permits it only while the local service is stopped.
func NewUIDirectEgressProbe() *UIEgressProbe {
	return newUIEgressProbe(func() bool { return true }, nil)
}

func (s *CommandServer) NewUIEgressProbe() (*UIEgressProbe, error) {
	uiCaptureLifecycle.Lock()
	defer uiCaptureLifecycle.Unlock()
	instance := s.Instance()
	if instance == nil {
		return nil, fmt.Errorf("service is not running")
	}
	probe := newUIEgressProbe(func() bool { return s.Instance() == instance },
		func(ctx context.Context, conn net.Conn, address string, onClose func(error)) {
			instance.Box().Router().RouteConnectionEx(ctx, conn, adapter.InboundContext{
				Inbound: "b-box-dashboard", Network: "tcp", Source: M.ParseSocksaddr("127.0.0.1:0"), Destination: M.ParseSocksaddr(address),
			}, onClose)
		})
	s.uiEgressProbes.add(probe)
	return probe, nil
}

func newUIEgressProbe(current func() bool, route func(context.Context, net.Conn, string, func(error))) *UIEgressProbe {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := NewHTTPClient().(*httpClient)
	wrapped.ModernTLS()
	wrapped.transport.TLSHandshakeTimeout = 0
	wrapped.client.CheckRedirect = uiRedirectLoop
	probe := &UIEgressProbe{
		ctx:         ctx,
		cancel:      cancel,
		client:      &wrapped.client,
		closeClient: wrapped.Close,
		current:     current,
		routes:      newUIRouteLifecycle(ctx),
	}
	// Each request owns its connection and deadline. A pooled connection
	// must not survive the IP race and carry the subsequent quality lookup.
	wrapped.transport.DisableKeepAlives = true
	direct := &net.Dialer{}
	wrapped.transport.DialContext = func(dialContext context.Context, network, address string) (net.Conn, error) {
		if !probe.current() {
			return nil, fmt.Errorf("service changed during egress check")
		}
		requestContext, ok := dialContext.Value(uiEgressRequestContextKey{}).(context.Context)
		if !ok {
			return nil, fmt.Errorf("missing probe context")
		}
		if err := requestContext.Err(); err != nil {
			return nil, err
		}
		// Direct is an explicit session mode, never a fallback after route failure.
		if route == nil {
			return direct.DialContext(requestContext, network, address)
		}
		return probe.routes.route(requestContext, func(ctx context.Context, conn net.Conn, onClose func(error)) {
			route(ctx, conn, address, onClose)
		})
	}
	return probe
}

func (p *UIEgressProbe) Close() {
	p.once.Do(func() {
		p.routes.close()
		p.cancel()
		p.closeClient()
		if p.onClose != nil {
			// Android can cancel a probe from the UI thread. Keep it registered
			// until routing exits so concurrent service teardown still waits.
			go func() {
				p.routes.wait()
				p.onClose()
			}()
		}
	})
}

// Get retains HTTP status so the quality lookup can distinguish throttling,
// unavailable sources and responses without a declared address type.
func (p *UIEgressProbe) Get(link string, timeoutMillis int64) (*UIHTTPResponse, error) {
	if timeoutMillis != -1 && (timeoutMillis <= 0 || timeoutMillis > math.MaxInt64/int64(time.Millisecond)) {
		return nil, fmt.Errorf("probe timeout must be positive and representable as a duration, or -1 for unlimited")
	}
	ctx, cancel := context.WithCancel(p.ctx)
	if timeoutMillis > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(p.ctx, time.Duration(timeoutMillis)*time.Millisecond)
	}
	defer cancel()
	return p.get(ctx, link)
}

func (p *UIEgressProbe) get(ctx context.Context, link string) (*UIHTTPResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.current() {
		return nil, fmt.Errorf("service changed during egress check")
	}
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Hostname() == "" || root.User != nil || root.Fragment != "" ||
		(root.Scheme != "http" && root.Scheme != "https") {
		return nil, fmt.Errorf("invalid probe address")
	}
	// net/http may detach the dial context's cancellation for connection reuse;
	// retain the owning request context for both direct and routed dials.
	requestContext := context.WithValue(ctx, uiEgressRequestContextKey{}, ctx)
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", uiProbeUserAgent)
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if !p.current() {
		return nil, fmt.Errorf("service changed during egress check")
	}
	return &UIHTTPResponse{Status: int32(response.StatusCode), Content: content}, nil
}

func (p *UIEgressProbe) LookupOutboundIP() (string, error) {
	source, info, err := lookupUIOutboundIP(p.ctx, uiIPSources, uiIPStagger, func(ctx context.Context, source uiIPSource) (*UIHTTPResponse, error) {
		return p.get(ctx, source.URL)
	})
	if err != nil {
		return "", err
	}
	root, _ := url.Parse(source.URL)
	info["ipSource"] = root.Hostname()
	info["checkedAt"] = time.Now().UnixMilli()
	encoded, err := json.Marshal(info)
	return string(encoded), err
}

type uiIPAnswer struct {
	source uiIPSource
	info   map[string]any
}

func lookupUIOutboundIP(ctx context.Context, sources []uiIPSource, stagger time.Duration,
	fetch func(context.Context, uiIPSource) (*UIHTTPResponse, error),
) (uiIPSource, map[string]any, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	answers := make(chan *uiIPAnswer, len(sources))
	next, inFlight := 0, 0
	start := func() {
		if next >= len(sources) {
			return
		}
		source := sources[next]
		next++
		inFlight++
		go func() {
			response, err := fetch(ctx, source)
			if err == nil && response != nil && response.Status == http.StatusOK {
				// Validate the full source response before it can win and cancel peers.
				info, err := parseUIOutboundIP(source, string(response.Content))
				if err == nil {
					answers <- &uiIPAnswer{source: source, info: info}
					return
				}
			}
			answers <- nil
		}()
	}
	start()
	ticker := time.NewTicker(stagger)
	defer ticker.Stop()
	for inFlight > 0 {
		select {
		case answer := <-answers:
			inFlight--
			if ctx.Err() != nil {
				return uiIPSource{}, nil, ctx.Err()
			}
			if answer != nil {
				return answer.source, answer.info, nil
			}
			start()
		case <-ticker.C:
			start()
		case <-ctx.Done():
			return uiIPSource{}, nil, ctx.Err()
		}
	}
	return uiIPSource{}, nil, fmt.Errorf("no egress IP source is available")
}

var uiSharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func uiPublicIP(text string) bool {
	address, err := netip.ParseAddr(text)
	if err != nil {
		return false
	}
	address = address.Unmap()
	return address.IsGlobalUnicast() && !address.IsPrivate() && !uiSharedAddressSpace.Contains(address)
}

func parseUIOutboundIP(source uiIPSource, body string) (map[string]any, error) {
	var ip, country string
	var hasCountry bool
	if source.CountryKey == "loc" {
		for _, line := range strings.FieldsFunc(body, func(r rune) bool { return r == '\n' || r == '\r' }) {
			key, value, found := strings.Cut(line, "=")
			if found && key == "ip" {
				ip = value
			}
			if found && key == "loc" {
				country = value
				hasCountry = true
			}
		}
	} else {
		var data map[string]any
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			return nil, err
		}
		ip, _ = data["ip"].(string)
		if source.CountryKey == "location.country_code" {
			if location, ok := data["location"].(map[string]any); ok {
				country, hasCountry = location["country_code"].(string)
			}
		} else {
			country, hasCountry = data[source.CountryKey].(string)
		}
	}
	if !uiPublicIP(ip) || !hasCountry {
		return nil, fmt.Errorf("invalid egress response")
	}
	return map[string]any{"ip": ip, "country": country}, nil
}
