package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	maxFrameSize = 65535
	headerSize   = 3 // 2B length + 1B type
)

// Frame is a single protocol frame on the wire.
type Frame struct {
	Type    Command
	Payload []byte
}

// WriteFrame writes a frame: [2B length LE][1B type][payload].
// Length includes the type byte.
func WriteFrame(w io.Writer, f *Frame) error {
	totalLen := 1 + len(f.Payload) // type + payload
	if totalLen > maxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", totalLen, maxFrameSize)
	}

	header := [headerSize]byte{}
	binary.LittleEndian.PutUint16(header[:2], uint16(totalLen))
	header[2] = byte(f.Type)

	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a frame from the wire.
func ReadFrame(r io.Reader) (*Frame, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}

	totalLen := int(binary.LittleEndian.Uint16(header[:2]))
	if totalLen < 1 {
		return nil, fmt.Errorf("invalid frame length: %d", totalLen)
	}
	if totalLen > maxFrameSize {
		return nil, fmt.Errorf("frame too large: %d", totalLen)
	}

	f := &Frame{
		Type: Command(header[2]),
	}

	payloadLen := totalLen - 1
	if payloadLen > 0 {
		f.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return nil, fmt.Errorf("read frame payload: %w", err)
		}
	}

	return f, nil
}

// EncodeConnectPayload encodes a CONNECT request payload: [addrType][addr][port BE].
func EncodeConnectPayload(host string, port uint16) []byte {
	ip4 := parseIPv4(host)
	ip6 := parseIPv6(host)

	var buf []byte
	switch {
	case ip4 != nil:
		buf = make([]byte, 1+4+2)
		buf[0] = byte(AddrIPv4)
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
	case ip6 != nil:
		buf = make([]byte, 1+16+2)
		buf[0] = byte(AddrIPv6)
		copy(buf[1:17], ip6)
		binary.BigEndian.PutUint16(buf[17:], port)
	default:
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

// DecodeConnectPayload parses a CONNECT request payload.
func DecodeConnectPayload(data []byte) (host string, port uint16, err error) {
	if len(data) < 1 {
		return "", 0, fmt.Errorf("connect payload too short")
	}

	addrType := AddrType(data[0])
	switch addrType {
	case AddrIPv4:
		if len(data) < 1+4+2 {
			return "", 0, fmt.Errorf("ipv4 payload too short")
		}
		host = fmt.Sprintf("%d.%d.%d.%d", data[1], data[2], data[3], data[4])
		port = binary.BigEndian.Uint16(data[5:7])
	case AddrIPv6:
		if len(data) < 1+16+2 {
			return "", 0, fmt.Errorf("ipv6 payload too short")
		}
		host = fmt.Sprintf("[%x:%x:%x:%x:%x:%x:%x:%x]",
			binary.BigEndian.Uint16(data[1:3]),
			binary.BigEndian.Uint16(data[3:5]),
			binary.BigEndian.Uint16(data[5:7]),
			binary.BigEndian.Uint16(data[7:9]),
			binary.BigEndian.Uint16(data[9:11]),
			binary.BigEndian.Uint16(data[11:13]),
			binary.BigEndian.Uint16(data[13:15]),
			binary.BigEndian.Uint16(data[15:17]))
		port = binary.BigEndian.Uint16(data[17:19])
	case AddrDomain:
		if len(data) < 2 {
			return "", 0, fmt.Errorf("domain payload too short")
		}
		nameLen := int(data[1])
		if len(data) < 2+nameLen+2 {
			return "", 0, fmt.Errorf("domain payload too short for name length %d", nameLen)
		}
		host = string(data[2 : 2+nameLen])
		port = binary.BigEndian.Uint16(data[2+nameLen : 2+nameLen+2])
	default:
		return "", 0, fmt.Errorf("unknown address type: 0x%02x", addrType)
	}
	return host, port, nil
}

func parseIPv4(s string) []byte {
	var a, b, c, d byte
	n, _ := fmt.Sscanf(s, "%d.%d.%d.%d", &a, &b, &c, &d)
	if n != 4 {
		return nil
	}
	return []byte{a, b, c, d}
}

func parseIPv6(s string) []byte {
	// Simplified: only detect bracket-wrapped full IPv6
	if len(s) < 2 || s[0] != '[' {
		return nil
	}
	// For now, return nil — IPv6 encoding handled by net package in production
	return nil
}
