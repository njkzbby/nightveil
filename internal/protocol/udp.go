package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

// UDPMessage represents a single UDP datagram relayed through the tunnel.
type UDPMessage struct {
	Host    string
	Port    uint16
	Payload []byte
}

// UDPRelay reads/writes UDP messages over a tunnel connection.
// Wire format per message:
//
//	[2B total_len LE][1B addr_type][addr][2B port BE][payload]
type UDPRelay struct {
	conn io.ReadWriter
	mu   sync.Mutex // protects writes
}

// NewUDPRelay creates a UDP relay over an existing tunnel connection.
func NewUDPRelay(conn io.ReadWriter) *UDPRelay {
	return &UDPRelay{conn: conn}
}

// WriteMessage sends a UDP datagram through the tunnel.
func (r *UDPRelay) WriteMessage(msg *UDPMessage) error {
	// Build address portion
	addrBytes := encodeAddr(msg.Host, msg.Port)

	// Total: addrBytes + payload
	totalLen := len(addrBytes) + len(msg.Payload)
	if totalLen > 65535 {
		return fmt.Errorf("udp message too large: %d", totalLen)
	}

	// Write: [2B total_len][addr][payload]
	header := make([]byte, 2)
	binary.LittleEndian.PutUint16(header, uint16(totalLen))

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := r.conn.Write(header); err != nil {
		return err
	}
	if _, err := r.conn.Write(addrBytes); err != nil {
		return err
	}
	if _, err := r.conn.Write(msg.Payload); err != nil {
		return err
	}
	return nil
}

// ReadMessage reads a UDP datagram from the tunnel.
func (r *UDPRelay) ReadMessage() (*UDPMessage, error) {
	// Read length header
	var header [2]byte
	if _, err := io.ReadFull(r.conn, header[:]); err != nil {
		return nil, fmt.Errorf("read udp header: %w", err)
	}
	totalLen := int(binary.LittleEndian.Uint16(header[:]))
	if totalLen < 4 { // minimum: 1B type + 1B + 2B port
		return nil, fmt.Errorf("udp message too short: %d", totalLen)
	}

	// Read entire message
	buf := make([]byte, totalLen)
	if _, err := io.ReadFull(r.conn, buf); err != nil {
		return nil, fmt.Errorf("read udp body: %w", err)
	}

	// Parse address
	host, port, addrLen, err := decodeAddr(buf)
	if err != nil {
		return nil, fmt.Errorf("parse udp addr: %w", err)
	}

	payload := buf[addrLen:]

	return &UDPMessage{
		Host:    host,
		Port:    port,
		Payload: payload,
	}, nil
}

// encodeAddr encodes [addr_type][addr][port BE]
func encodeAddr(host string, port uint16) []byte {
	ip4 := parseIPv4(host)

	var buf []byte
	if ip4 != nil {
		buf = make([]byte, 1+4+2)
		buf[0] = byte(AddrIPv4)
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
	} else if ip := net.ParseIP(host); ip != nil && ip.To16() != nil && ip.To4() == nil {
		buf = make([]byte, 1+16+2)
		buf[0] = byte(AddrIPv6)
		copy(buf[1:17], ip.To16())
		binary.BigEndian.PutUint16(buf[17:], port)
	} else {
		// Domain
		nameBytes := []byte(host)
		buf = make([]byte, 1+1+len(nameBytes)+2)
		buf[0] = byte(AddrDomain)
		buf[1] = byte(len(nameBytes))
		copy(buf[2:2+len(nameBytes)], nameBytes)
		binary.BigEndian.PutUint16(buf[2+len(nameBytes):], port)
	}
	return buf
}

// decodeAddr parses [addr_type][addr][port BE], returns host, port, bytes consumed.
func decodeAddr(data []byte) (string, uint16, int, error) {
	if len(data) < 1 {
		return "", 0, 0, fmt.Errorf("empty addr data")
	}

	addrType := AddrType(data[0])
	switch addrType {
	case AddrIPv4:
		if len(data) < 1+4+2 {
			return "", 0, 0, fmt.Errorf("ipv4 addr too short")
		}
		host := fmt.Sprintf("%d.%d.%d.%d", data[1], data[2], data[3], data[4])
		port := binary.BigEndian.Uint16(data[5:7])
		return host, port, 7, nil

	case AddrIPv6:
		if len(data) < 1+16+2 {
			return "", 0, 0, fmt.Errorf("ipv6 addr too short")
		}
		ip := net.IP(data[1:17])
		port := binary.BigEndian.Uint16(data[17:19])
		return ip.String(), port, 19, nil

	case AddrDomain:
		if len(data) < 2 {
			return "", 0, 0, fmt.Errorf("domain addr too short")
		}
		nameLen := int(data[1])
		if len(data) < 2+nameLen+2 {
			return "", 0, 0, fmt.Errorf("domain name truncated")
		}
		host := string(data[2 : 2+nameLen])
		port := binary.BigEndian.Uint16(data[2+nameLen : 2+nameLen+2])
		return host, port, 2 + nameLen + 2, nil

	default:
		return "", 0, 0, fmt.Errorf("unknown addr type: 0x%02x", addrType)
	}
}
