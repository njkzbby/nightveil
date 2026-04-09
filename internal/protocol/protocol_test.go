package protocol

import (
	"context"
	"net"
	"testing"
)

func TestRequestAddress(t *testing.T) {
	tests := []struct {
		host string
		port uint16
		want string
	}{
		{"example.com", 443, "example.com:443"},
		{"1.2.3.4", 80, "1.2.3.4:80"},
		{"::1", 8080, "[::1]:8080"},
	}

	for _, tt := range tests {
		r := &Request{Host: tt.host, Port: tt.port}
		if got := r.Address(); got != tt.want {
			t.Errorf("Address(%s, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestClientServerHandshakeServerOnly(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	sConn := &pipeTransportConn{serverConn}
	server := NewServer()

	errCh := make(chan error, 1)
	reqCh := make(chan *Request, 1)

	go func() {
		req, err := server.HandleConnection(context.Background(), sConn)
		errCh <- err
		reqCh <- req
	}()

	// Manually send CONNECT frame
	payload := EncodeConnectPayload("google.com", 443)
	WriteFrame(clientConn, &Frame{Type: CmdConnect, Payload: payload})

	if err := <-errCh; err != nil {
		t.Fatalf("server HandleConnection: %v", err)
	}
	gotReq := <-reqCh
	if gotReq.Host != "google.com" || gotReq.Port != 443 {
		t.Fatalf("got %s:%d, want google.com:443", gotReq.Host, gotReq.Port)
	}

	clientConn.Close()
	serverConn.Close()
}

func TestClientServerFullHandshake(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	cConn := &pipeTransportConn{clientConn}
	sConn := &pipeTransportConn{serverConn}

	client := NewClient()
	server := NewServer()

	// Server: read CONNECT, send ACK OK
	serverDone := make(chan error, 1)
	go func() {
		req, err := server.HandleConnection(context.Background(), sConn)
		if err != nil {
			serverDone <- err
			return
		}
		if req.Host != "example.com" || req.Port != 80 {
			serverDone <- err
			return
		}
		serverDone <- SendACK(sConn, StatusOK)
	}()

	// Client: send CONNECT, read ACK
	req := &Request{Command: CmdConnect, Host: "example.com", Port: 80}
	err := client.Handshake(context.Background(), cConn, req)
	if err != nil {
		t.Fatalf("client handshake: %v", err)
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	clientConn.Close()
	serverConn.Close()
}

func TestServerRefused(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	cConn := &pipeTransportConn{clientConn}
	sConn := &pipeTransportConn{serverConn}

	client := NewClient()
	server := NewServer()

	go func() {
		server.HandleConnection(context.Background(), sConn)
		SendACK(sConn, StatusRefused)
	}()

	req := &Request{Command: CmdConnect, Host: "blocked.com", Port: 443}
	err := client.Handshake(context.Background(), cConn, req)
	if err == nil {
		t.Fatal("expected error for refused connection")
	}

	clientConn.Close()
	serverConn.Close()
}

func TestSendACKStatuses(t *testing.T) {
	for _, status := range []Status{StatusOK, StatusRefused, StatusUnreachable} {
		clientConn, serverConn := net.Pipe()

		go func() {
			SendACK(&pipeTransportConn{serverConn}, status)
			serverConn.Close()
		}()

		f, err := ReadFrame(clientConn)
		if err != nil {
			t.Fatalf("status %d: read: %v", status, err)
		}
		if f.Type != CmdACK || len(f.Payload) < 1 || Status(f.Payload[0]) != status {
			t.Fatalf("status %d: got type=%d payload=%x", status, f.Type, f.Payload)
		}
		clientConn.Close()
	}
}

func TestServerBadCommand(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	sConn := &pipeTransportConn{serverConn}

	// Send a DATA frame instead of CONNECT
	go func() {
		WriteFrame(clientConn, &Frame{Type: CmdData, Payload: []byte("nope")})
		clientConn.Close()
	}()

	server := NewServer()
	_, err := server.HandleConnection(context.Background(), sConn)
	if err == nil {
		t.Fatal("expected error for non-CONNECT frame")
	}
	serverConn.Close()
}

func TestServerBadPayload(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	sConn := &pipeTransportConn{serverConn}

	// Send CONNECT with garbage payload
	go func() {
		WriteFrame(clientConn, &Frame{Type: CmdConnect, Payload: []byte{0xFF}})
		clientConn.Close()
	}()

	server := NewServer()
	_, err := server.HandleConnection(context.Background(), sConn)
	if err == nil {
		t.Fatal("expected error for bad connect payload")
	}
	serverConn.Close()
}

func TestConnectPayloadVariousAddresses(t *testing.T) {
	tests := []struct {
		name string
		host string
		port uint16
	}{
		{"domain short", "a.io", 1},
		{"domain long", "very.long.subdomain.example.com", 65535},
		{"ipv4 localhost", "127.0.0.1", 8080},
		{"ipv4 broadcast", "255.255.255.255", 443},
		{"ipv4 zero", "0.0.0.0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := EncodeConnectPayload(tt.host, tt.port)
			host, port, err := DecodeConnectPayload(payload)
			if err != nil {
				t.Fatal(err)
			}
			if host != tt.host || port != tt.port {
				t.Fatalf("got %s:%d, want %s:%d", host, port, tt.host, tt.port)
			}
		})
	}
}

func TestDecodeConnectPayloadErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"unknown type", []byte{0x99}},
		{"ipv4 too short", []byte{0x01, 1, 2}},
		{"domain no length", []byte{0x03}},
		{"domain truncated", []byte{0x03, 10, 'a', 'b'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DecodeConnectPayload(tt.data)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// pipeTransportConn wraps net.Conn for transport.Conn interface
type pipeTransportConn struct{ net.Conn }

func (p *pipeTransportConn) LocalAddr() net.Addr  { return p.Conn.LocalAddr() }
func (p *pipeTransportConn) RemoteAddr() net.Addr { return p.Conn.RemoteAddr() }
