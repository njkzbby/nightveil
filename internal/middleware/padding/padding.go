// Package padding implements a random padding middleware.
// Wire format: [2B real_length LE][2B pad_length LE][real_data][random_padding]
package padding

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"

	"github.com/njkzbby/nightveil/internal/transport"
)

const headerSize = 4 // 2B real_len + 2B pad_len

// Config for the padding middleware.
type Config struct {
	MinBytes int
	MaxBytes int
}

// Middleware implements random padding.
type Middleware struct {
	config Config
}

func New(cfg Config) *Middleware {
	if cfg.MinBytes < 0 {
		cfg.MinBytes = 0
	}
	if cfg.MaxBytes < cfg.MinBytes {
		cfg.MaxBytes = cfg.MinBytes
	}
	return &Middleware{config: cfg}
}

func (m *Middleware) Wrap(conn transport.Conn) transport.Conn {
	return &paddingConn{Conn: conn, config: m.config}
}

type paddingConn struct {
	transport.Conn
	config Config
	rMu    sync.Mutex
	rBuf   []byte // leftover real data from partial reads
}

// Write: [2B real_len][2B pad_len][real_data][random_padding]
func (c *paddingConn) Write(p []byte) (int, error) {
	if len(p) > 65535 {
		written := 0
		for written < len(p) {
			end := written + 65535
			if end > len(p) {
				end = len(p)
			}
			if err := c.writeFrame(p[written:end]); err != nil {
				return written, err
			}
			written = end
		}
		return len(p), nil
	}
	return len(p), c.writeFrame(p)
}

func (c *paddingConn) writeFrame(data []byte) error {
	padLen := c.randomPadLen()

	frame := make([]byte, headerSize+len(data)+padLen)
	binary.LittleEndian.PutUint16(frame[0:2], uint16(len(data)))
	binary.LittleEndian.PutUint16(frame[2:4], uint16(padLen))
	copy(frame[headerSize:], data)
	if padLen > 0 {
		rand.Read(frame[headerSize+len(data):])
	}

	_, err := c.Conn.Write(frame)
	return err
}

// Read: parse header, extract real data, discard padding.
func (c *paddingConn) Read(p []byte) (int, error) {
	c.rMu.Lock()
	defer c.rMu.Unlock()

	if len(c.rBuf) > 0 {
		n := copy(p, c.rBuf)
		c.rBuf = c.rBuf[n:]
		return n, nil
	}

	var header [headerSize]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		return 0, fmt.Errorf("read padding header: %w", err)
	}

	realLen := int(binary.LittleEndian.Uint16(header[0:2]))
	padLen := int(binary.LittleEndian.Uint16(header[2:4]))

	// Read real data + padding together, discard padding
	totalLen := realLen + padLen
	buf := make([]byte, totalLen)
	if _, err := io.ReadFull(c.Conn, buf); err != nil {
		return 0, fmt.Errorf("read padded payload: %w", err)
	}

	realData := buf[:realLen]
	n := copy(p, realData)
	if n < realLen {
		c.rBuf = make([]byte, realLen-n)
		copy(c.rBuf, realData[n:])
	}
	return n, nil
}

func (c *paddingConn) LocalAddr() net.Addr  { return c.Conn.LocalAddr() }
func (c *paddingConn) RemoteAddr() net.Addr { return c.Conn.RemoteAddr() }

func (c *paddingConn) randomPadLen() int {
	if c.config.MaxBytes <= c.config.MinBytes {
		return c.config.MinBytes
	}
	diff := c.config.MaxBytes - c.config.MinBytes
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(diff+1)))
	return c.config.MinBytes + int(n.Int64())
}
