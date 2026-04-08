package protocol

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		frame   Frame
	}{
		{"connect", Frame{Type: CmdConnect, Payload: []byte{0x03, 0x0B, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 0x00, 0x50}}},
		{"ack ok", Frame{Type: CmdACK, Payload: []byte{byte(StatusOK)}}},
		{"ack refused", Frame{Type: CmdACK, Payload: []byte{byte(StatusRefused)}}},
		{"data small", Frame{Type: CmdData, Payload: []byte("hello world")}},
		{"data empty", Frame{Type: CmdData, Payload: nil}},
		{"close", Frame{Type: CmdClose, Payload: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, &tt.frame); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}

			got, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}

			if got.Type != tt.frame.Type {
				t.Errorf("type: got %d, want %d", got.Type, tt.frame.Type)
			}
			if !bytes.Equal(got.Payload, tt.frame.Payload) {
				t.Errorf("payload: got %x, want %x", got.Payload, tt.frame.Payload)
			}
		})
	}
}

func TestFrameLargePayload(t *testing.T) {
	payload := make([]byte, 32000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	var buf bytes.Buffer
	f := &Frame{Type: CmdData, Payload: payload}
	if err := WriteFrame(&buf, f); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatal("large payload mismatch")
	}
}

func TestMultipleFrames(t *testing.T) {
	var buf bytes.Buffer

	frames := []Frame{
		{Type: CmdConnect, Payload: EncodeConnectPayload("example.com", 443)},
		{Type: CmdACK, Payload: []byte{byte(StatusOK)}},
		{Type: CmdData, Payload: []byte("GET / HTTP/1.1\r\n")},
		{Type: CmdClose, Payload: nil},
	}

	for i := range frames {
		if err := WriteFrame(&buf, &frames[i]); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}
	}

	for i := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if got.Type != frames[i].Type {
			t.Fatalf("frame %d type mismatch", i)
		}
		if !bytes.Equal(got.Payload, frames[i].Payload) {
			t.Fatalf("frame %d payload mismatch", i)
		}
	}
}

func TestConnectPayloadDomain(t *testing.T) {
	payload := EncodeConnectPayload("google.com", 443)
	host, port, err := DecodeConnectPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != "google.com" || port != 443 {
		t.Fatalf("got %s:%d, want google.com:443", host, port)
	}
}

func TestConnectPayloadIPv4(t *testing.T) {
	payload := EncodeConnectPayload("1.2.3.4", 8080)
	host, port, err := DecodeConnectPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != "1.2.3.4" || port != 8080 {
		t.Fatalf("got %s:%d, want 1.2.3.4:8080", host, port)
	}
}
