package route

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// Route sniffing can leave a cache below the tracker before capture is selected.
// Keep the native replay and ownership interfaces, but serialize cache access
// with service/route closure. Network I/O must never hold the cache lock.
type debugCachedConn struct {
	net.Conn
	mu     sync.Mutex
	taken  bool
	buffer *buf.Buffer
}

func newDebugCachedConn(conn net.Conn, buffer *buf.Buffer) *debugCachedConn {
	buffer.IncRef()
	return &debugCachedConn{Conn: conn, buffer: buffer}
}

func (c *debugCachedConn) ReadCached() *buf.Buffer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.taken {
		return nil
	}
	c.taken = true
	return c.takeBufferLocked()
}

// The caller owns the detached buffer and must release it. A concurrent Close
// cannot free bytes that a WriteTo or the native copier is still forwarding.
func (c *debugCachedConn) takeBufferLocked() *buf.Buffer {
	buffer := c.buffer
	c.buffer = nil
	if buffer != nil {
		buffer.DecRef()
	}
	return buffer
}

func (c *debugCachedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if c.buffer != nil {
		n, err := c.buffer.Read(p)
		if err == nil {
			c.mu.Unlock()
			return n, nil
		}
		c.takeBufferLocked().Release()
	}
	c.mu.Unlock()
	return c.Conn.Read(p)
}

func (c *debugCachedConn) WriteTo(w io.Writer) (n int64, err error) {
	c.mu.Lock()
	buffer := c.takeBufferLocked()
	c.mu.Unlock()
	if buffer != nil {
		data := buffer.Bytes()
		written, writeErr := w.Write(data)
		if written < 0 || written > len(data) {
			written = 0
			if writeErr == nil {
				writeErr = errors.New("invalid cached write count")
			}
		}
		n = int64(written)
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			// A failed writer must not silently consume the unwritten suffix.
			// Restore it only while this connection still owns its cache.
			buffer.Advance(written)
			c.mu.Lock()
			if !buffer.IsEmpty() && !c.taken && c.buffer == nil {
				buffer.IncRef()
				c.buffer, buffer = buffer, nil
			}
			c.mu.Unlock()
			buffer.Release()
			return n, writeErr
		}
		buffer.Release()
	}
	cn, err := bufio.Copy(w, c.Conn)
	return n + cn, err
}

func (c *debugCachedConn) ReadFrom(r io.Reader) (int64, error) {
	return bufio.Copy(c.Conn, r)
}

func (c *debugCachedConn) Upstream() any { return c.Conn }

func (c *debugCachedConn) ReaderReplaceable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer == nil
}

func (c *debugCachedConn) WriterReplaceable() bool { return true }

func (c *debugCachedConn) CloseWrite() error {
	writer, _ := N.UnwrapCountWriter(c.Conn, nil)
	if half, loaded := N.UnwrapWriter(writer).(N.WriteCloser); loaded {
		return half.CloseWrite()
	}
	return c.Close()
}

func (c *debugCachedConn) Close() error {
	c.mu.Lock()
	if !c.taken {
		c.taken = true
		c.takeBufferLocked().Release()
	}
	c.mu.Unlock()
	return c.Conn.Close()
}
