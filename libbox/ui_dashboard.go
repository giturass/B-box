package libbox

// B-box UI bindings for existing Go runtime statistics and sing-box routing.
// This adds no listener, routing rule, outbound or Clash implementation.
import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

type UIRuntimeMemory struct {
	Total        int64
	HeapInuse    int64
	HeapIdle     int64
	StackInuse   int64
	RuntimeOther int64
	Goroutines   int32
}

func ReadUIRuntimeMemory() *UIRuntimeMemory {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	total := stats.Sys - stats.HeapReleased
	heapIdle := stats.HeapIdle - stats.HeapReleased
	return &UIRuntimeMemory{
		Total: int64(total), HeapInuse: int64(stats.HeapInuse), HeapIdle: int64(heapIdle),
		StackInuse: int64(stats.StackInuse), Goroutines: int32(runtime.NumGoroutine()),
		RuntimeOther: int64(total - stats.HeapInuse - heapIdle - stats.StackInuse),
	}
}

func CollectUIRuntimeMemory() { debug.FreeOSMemory() }

// QueryUIEgress uses the same router as inbound connections. It never falls
// back to the device's direct network when routing fails.
func (s *CommandServer) QueryUIEgress(link string) (string, error) {
	instance := s.Instance()
	if instance == nil {
		return "", fmt.Errorf("service is not running")
	}
	client := NewHTTPClient().(*httpClient)
	defer client.Close()
	client.ModernTLS()
	client.client.Timeout = 10 * time.Second
	requestContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		local, routed := net.Pipe()
		stop := context.AfterFunc(requestContext, func() { local.Close(); routed.Close() })
		go instance.Box().Router().RouteConnectionEx(requestContext, routed, adapter.InboundContext{
			Inbound: "b-box-dashboard", Network: "tcp", Source: M.ParseSocksaddr("127.0.0.1:0"), Destination: M.ParseSocksaddr(address),
		}, func(error) { stop(); routed.Close() })
		return local, nil
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, link, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "B-box")
	response, err := client.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	if len(content) > 128*1024 {
		return "", fmt.Errorf("egress response exceeds 128 KiB")
	}
	return string(content), err
}
