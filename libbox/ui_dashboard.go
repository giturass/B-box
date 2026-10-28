package libbox

// B-box UI bindings for existing Go runtime statistics and sing-box routing.
// This adds no listener, routing rule, outbound or Clash implementation.
import (
	"runtime"
	"runtime/debug"
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
