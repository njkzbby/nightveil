package protocol

import (
	"bytes"
	"net"
	"testing"
)

func TestUDPRelayRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	writer := NewUDPRelay(left)
	reader := NewUDPRelay(right)

	msg := &UDPMessage{
		Host:    "8.8.8.8",
		Port:    53,
		Payload: []byte{0x00, 0x01, 0x02, 0x03}, // fake DNS query
	}

	go func() {
		writer.WriteMessage(msg)
	}()

	got, err := reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if got.Host != "8.8.8.8" || got.Port != 53 {
		t.Fatalf("addr: got %s:%d, want 8.8.8.8:53", got.Host, got.Port)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Fatalf("payload: got %x, want %x", got.Payload, msg.Payload)
	}
}

func TestUDPRelayDomain(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	writer := NewUDPRelay(left)
	reader := NewUDPRelay(right)

	msg := &UDPMessage{
		Host:    "discord.gg",
		Port:    443,
		Payload: []byte("hello discord"),
	}

	go func() {
		writer.WriteMessage(msg)
	}()

	got, err := reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if got.Host != "discord.gg" || got.Port != 443 {
		t.Fatalf("got %s:%d", got.Host, got.Port)
	}
	if string(got.Payload) != "hello discord" {
		t.Fatalf("payload: %q", got.Payload)
	}
}

func TestUDPRelayMultipleMessages(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	writer := NewUDPRelay(left)
	reader := NewUDPRelay(right)

	messages := []*UDPMessage{
		{Host: "1.1.1.1", Port: 53, Payload: []byte("query1")},
		{Host: "8.8.4.4", Port: 53, Payload: []byte("query2")},
		{Host: "example.com", Port: 443, Payload: []byte("data")},
	}

	go func() {
		for _, m := range messages {
			writer.WriteMessage(m)
		}
	}()

	for i, want := range messages {
		got, err := reader.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.Host != want.Host || got.Port != want.Port {
			t.Fatalf("message %d: got %s:%d, want %s:%d", i, got.Host, got.Port, want.Host, want.Port)
		}
		if !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("message %d: payload mismatch", i)
		}
	}
}

func TestUDPRelayLargePayload(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	writer := NewUDPRelay(left)
	reader := NewUDPRelay(right)

	// 1400 bytes — typical MTU-sized UDP packet
	payload := make([]byte, 1400)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	msg := &UDPMessage{Host: "10.0.0.1", Port: 9999, Payload: payload}

	go func() {
		writer.WriteMessage(msg)
	}()

	got, err := reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payload) != 1400 {
		t.Fatalf("payload length: %d", len(got.Payload))
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatal("payload mismatch")
	}
}

func TestEncodeDecodeAddr(t *testing.T) {
	tests := []struct {
		host string
		port uint16
	}{
		{"1.2.3.4", 53},
		{"255.255.255.255", 65535},
		{"example.com", 443},
		{"sub.domain.co.uk", 8080},
	}

	for _, tt := range tests {
		encoded := encodeAddr(tt.host, tt.port)
		host, port, _, err := decodeAddr(encoded)
		if err != nil {
			t.Fatalf("decodeAddr(%s:%d): %v", tt.host, tt.port, err)
		}
		if host != tt.host || port != tt.port {
			t.Fatalf("got %s:%d, want %s:%d", host, port, tt.host, tt.port)
		}
	}
}

func TestDecodeAddrErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"unknown type", []byte{0x99}},
		{"ipv4 short", []byte{0x01, 1, 2}},
		{"domain short", []byte{0x03}},
		{"domain truncated", []byte{0x03, 10, 'a'}},
	}

	for _, tt := range tests {
		_, _, _, err := decodeAddr(tt.data)
		if err == nil {
			t.Fatalf("%s: expected error", tt.name)
		}
	}
}
