package xhttp

import (
	"errors"
	"io"
	"net"

	"github.com/njkzbby/nightveil/internal/session"
)

// splitConn combines a session's upload queue (read) and download pipe (write)
// into a single transport.Conn.
type splitConn struct {
	sess       *session.Session
	localAddr  net.Addr
	remoteAddr net.Addr

	// leftover holds bytes from the last ReadUpload chunk that didn't fit in
	// the caller's buffer. Subsequent Read calls drain this first before
	// pulling another chunk. Without this, callers using small read buffers
	// (e.g. TLS records, framing parsers) silently lose data — the previous
	// implementation truncated via copy() and discarded the remainder.
	leftover []byte
}

func newSplitConn(sess *session.Session, local, remote net.Addr) *splitConn {
	return &splitConn{
		sess:       sess,
		localAddr:  local,
		remoteAddr: remote,
	}
}

// Read pulls the next batch of uploaded bytes from the session.
//
// In stream-up mode the session feeds an io.Pipe directly from the long-lived
// upload POST body — Read just delegates to the pipe reader and translates
// EOF into ErrSessionClosed for the relay loop.
//
// In packet-up mode it drains any leftover bytes from the previous chunk
// first, then pulls the next in-order chunk from the session's reassembly
// queue. Always lossless.
//
// If a splitConn was created before any POST arrived (e.g. the GET handler
// fired tryEmit) and the first POST turns out to be stream-mode, ReadUpload
// returns false because the session has flipped into stream mode. We catch
// that and fall through to the stream path on the same call.
func (c *splitConn) Read(p []byte) (int, error) {
	if c.sess.IsStreamMode() {
		return c.readStream(p)
	}
	if len(c.leftover) > 0 {
		n := copy(p, c.leftover)
		c.leftover = c.leftover[n:]
		if len(c.leftover) == 0 {
			c.leftover = nil
		}
		return n, nil
	}
	data, ok := c.sess.ReadUpload()
	if !ok {
		// ReadUpload returns false either because the session closed OR
		// because it transitioned to stream mode. Re-check and switch.
		if c.sess.IsStreamMode() {
			return c.readStream(p)
		}
		return 0, session.ErrSessionClosed
	}
	n := copy(p, data)
	if n < len(data) {
		// Stash the remainder for the next Read.
		c.leftover = data[n:]
	}
	return n, nil
}

func (c *splitConn) readStream(p []byte) (int, error) {
	n, err := c.sess.StreamReader().Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			return n, session.ErrSessionClosed
		}
		return n, err
	}
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
