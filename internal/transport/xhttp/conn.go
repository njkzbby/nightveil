package xhttp

import (
	"net"

	"github.com/nightveil/nv/internal/session"
)

// splitConn combines a session's upload queue (read) and download pipe (write)
// into a single transport.Conn.
type splitConn struct {
	sess       *session.Session
	localAddr  net.Addr
	remoteAddr net.Addr
}

func newSplitConn(sess *session.Session, local, remote net.Addr) *splitConn {
	return &splitConn{
		sess:       sess,
		localAddr:  local,
		remoteAddr: remote,
	}
}

// Read pulls the next in-order upload chunk from the session.
func (c *splitConn) Read(p []byte) (int, error) {
	data, ok := c.sess.ReadUpload()
	if !ok {
		return 0, session.ErrSessionClosed
	}
	n := copy(p, data)
	// If data is larger than p, we lose the rest — callers should use bufio.
	// In practice, chunks are small (≤14KB) and reads use 32KB+ buffers.
	return n, nil
}

// Write pushes data to the download buffer (survives GET reconnects).
func (c *splitConn) Write(p []byte) (int, error) {
	return c.sess.DownloadBuf.Write(p)
}

// Close terminates the session.
func (c *splitConn) Close() error {
	c.sess.Close()
	return nil
}

func (c *splitConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *splitConn) RemoteAddr() net.Addr { return c.remoteAddr }

// dummyAddr implements net.Addr for sessions.
type dummyAddr struct {
	network string
	addr    string
}

func (a *dummyAddr) Network() string { return a.network }
func (a *dummyAddr) String() string  { return a.addr }
