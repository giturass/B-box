package httpdebug

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"

	"github.com/dop251/goja"
)

const maxScriptUngzipBytes = 16 * 1024 * 1024

// Results may remain reachable in JavaScript until the invocation ends. Hold
// every successful result's lease until VM cleanup, rather than only bounding
// the transient decompressor. No host I/O or platform identity is fabricated.
func installScriptUtils(vm *goja.Runtime, ctx context.Context) func() {
	var leases []*bodyLease
	_ = vm.Set("$utils", map[string]any{"ungzip": func(call goja.FunctionCall) goja.Value {
		if ctx.Err() != nil {
			return goja.Null()
		}
		lease := reserveBodyWork(0)
		if lease == nil {
			return goja.Null()
		}
		retained := false
		defer func() {
			if !retained {
				lease.release()
			}
		}()
		input := call.Argument(0)
		data, ok := input.Export().([]byte)
		if !ok || int64(len(data)) > MaxRewriteRequestBody || !lease.growWork(int64(len(data))) {
			return goja.Null()
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return goja.Null()
		}
		defer reader.Close()
		decoded, err := readScriptHTTPBody(scriptContextReader{ctx, reader}, maxScriptUngzipBytes, lease, int64(len(data)))
		if err != nil || ctx.Err() != nil {
			return goja.Null()
		}
		result := scriptBodyValue(vm, decoded, true).(*goja.Object)
		leases = append(leases, lease)
		retained = true
		return result
	}})
	return func() {
		for _, lease := range leases {
			lease.release()
		}
		leases = nil
	}
}

type scriptContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r scriptContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
