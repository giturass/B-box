package route

import (
	"errors"
	"net"
	"time"
)

// A raw TCP route needs EOF in each direction independently: the origin may
// wait for request EOF before sending its final response. The pinned sing pipe
// and net.Pipe only close a full connection, so dedicate one pipe to each lane.
func newDebugStreamPipe() (net.Conn, net.Conn) {
	clientRead, serverWrite := net.Pipe()
	serverRead, clientWrite := net.Pipe()
	return &debugStreamConn{read: clientRead, write: clientWrite},
		&debugStreamConn{read: serverRead, write: serverWrite}
}

type debugStreamConn struct {
	read, write net.Conn
}

func (c *debugStreamConn) Read(p []byte) (int, error)  { return c.read.Read(p) }
func (c *debugStreamConn) Write(p []byte) (int, error) { return c.write.Write(p) }
func (c *debugStreamConn) LocalAddr() net.Addr         { return c.read.LocalAddr() }
func (c *debugStreamConn) RemoteAddr() net.Addr        { return c.write.RemoteAddr() }
func (c *debugStreamConn) CloseRead() error            { return c.read.Close() }
func (c *debugStreamConn) CloseWrite() error           { return c.write.Close() }
func (c *debugStreamConn) Close() error                { return errors.Join(c.read.Close(), c.write.Close()) }
func (c *debugStreamConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *debugStreamConn) SetReadDeadline(t time.Time) error { return c.read.SetReadDeadline(t) }
func (c *debugStreamConn) SetWriteDeadline(t time.Time) error {
	return c.write.SetWriteDeadline(t)
}
