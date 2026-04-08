// Package proxy implements local inbound listeners (SOCKS5) and relay logic.
package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// SOCKS5 constants
const (
	socks5Version = 0x05
	authNone      = 0x00
	cmdConnect    = 0x01
	atypIPv4      = 0x01
	atypDomain    = 0x03
	atypIPv6      = 0x04
	repSuccess    = 0x00
	repFailure    = 0x01
)

// SOCKS5Target holds the parsed destination from a SOCKS5 CONNECT.
type SOCKS5Target struct {
	Host string
	Port uint16
}

// SOCKS5Handshake performs the SOCKS5 handshake with the client.
// Returns the target address on success. Supports no-auth only.
func SOCKS5Handshake(conn net.Conn) (*SOCKS5Target, error) {
	// --- Auth negotiation ---
	// Client: [version][nmethods][methods...]
	buf := make([]byte, 258)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return nil, fmt.Errorf("read auth header: %w", err)
	}
	if buf[0] != socks5Version {
		return nil, fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nmethods]); err != nil {
		return nil, fmt.Errorf("read auth methods: %w", err)
	}

	// Reply: no-auth
	if _, err := conn.Write([]byte{socks5Version, authNone}); err != nil {
		return nil, fmt.Errorf("write auth reply: %w", err)
	}

	// --- Request ---
	// Client: [version][cmd][rsv][atyp][addr][port]
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return nil, fmt.Errorf("read request header: %w", err)
	}
	if buf[0] != socks5Version {
		return nil, fmt.Errorf("bad request version: %d", buf[0])
	}
	if buf[1] != cmdConnect {
		sendSOCKS5Reply(conn, 0x07) // command not supported
		return nil, fmt.Errorf("unsupported command: %d", buf[1])
	}

	atyp := buf[3]
	var host string
	switch atyp {
	case atypIPv4:
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return nil, err
		}
		host = fmt.Sprintf("%d.%d.%d.%d", buf[0], buf[1], buf[2], buf[3])
	case atypDomain:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return nil, err
		}
		nameLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:nameLen]); err != nil {
			return nil, err
		}
		host = string(buf[:nameLen])
	case atypIPv6:
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return nil, err
		}
		host = net.IP(buf[:16]).String()
	default:
		sendSOCKS5Reply(conn, 0x08) // address type not supported
		return nil, fmt.Errorf("unsupported address type: %d", atyp)
	}

	// Port (2 bytes big-endian)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return nil, fmt.Errorf("read port: %w", err)
	}
	port := binary.BigEndian.Uint16(buf[:2])

	return &SOCKS5Target{Host: host, Port: port}, nil
}

// SOCKS5SendSuccess sends a successful SOCKS5 reply.
func SOCKS5SendSuccess(conn net.Conn) error {
	return sendSOCKS5Reply(conn, repSuccess)
}

// SOCKS5SendFailure sends a failure SOCKS5 reply.
func SOCKS5SendFailure(conn net.Conn) error {
	return sendSOCKS5Reply(conn, repFailure)
}

func sendSOCKS5Reply(conn net.Conn, rep byte) error {
	// [version][rep][rsv][atyp=IPv4][addr=0.0.0.0][port=0]
	reply := []byte{socks5Version, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}
