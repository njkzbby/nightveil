package raw

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/njkzbby/nightveil/internal/protocol"
	"github.com/njkzbby/nightveil/internal/proxy"
)

func TestRawTransportRoundTrip(t *testing.T) {
	// Start raw server
	srv, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	addr := srv.Listener.Addr().String()

	// Server: accept one connection, echo back
	go func() {
		conn, err := srv.Accept(context.Background())
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn) // echo
	}()

	// Client: dial, write, read
	client := &Client{ServerAddr: addr}
	conn, err := client.Dial(context.Background(), [16]byte{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := []byte("hello nightveil")
	conn.Write(msg)

	buf := make([]byte, 100)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "hello nightveil" {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestE2ETunnel(t *testing.T) {
	// 1. Start a dummy target HTTP server
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetLn.Close()

	targetAddr := targetLn.Addr().String()
	targetResponse := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK"

	go func() {
		for {
			conn, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				conn.Read(buf) // read request
				conn.Write([]byte(targetResponse))
			}()
		}
	}()

	// 2. Start nightveil server (raw TCP)
	nvSrv, err := NewServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer nvSrv.Close()

	nvSrvAddr := nvSrv.Listener.Addr().String()
	proto := protocol.NewServer()

	go func() {
		for {
			conn, err := nvSrv.Accept(context.Background())
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := proto.HandleConnection(context.Background(), conn)
				if err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", req.Address(), 5*time.Second)
				if err != nil {
					protocol.SendACK(conn, protocol.StatusUnreachable)
					return
				}
				defer target.Close()
				protocol.SendACK(conn, protocol.StatusOK)
				proxy.Relay(conn, target)
			}()
		}
	}()

	// 3. Start SOCKS5 listener (nightveil client)
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()

	socksAddr := socksLn.Addr().String()
	transport := &Client{ServerAddr: nvSrvAddr}
	clientProto := protocol.NewClient()

	go func() {
		for {
			conn, err := socksLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				target, err := proxy.SOCKS5Handshake(conn)
				if err != nil {
					return
				}
				var sid [16]byte
				tConn, err := transport.Dial(context.Background(), sid)
				if err != nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				defer tConn.Close()
				req := &protocol.Request{Command: protocol.CmdConnect, Host: target.Host, Port: target.Port}
				if err := clientProto.Handshake(context.Background(), tConn, req); err != nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				proxy.SOCKS5SendSuccess(conn)
				proxy.Relay(conn, tConn)
			}()
		}
	}()

	// 4. Connect through SOCKS5 as a real client would
	// Parse target address for SOCKS5 request
	targetHost, targetPort, _ := net.SplitHostPort(targetAddr)

	socksConn, err := net.DialTimeout("tcp", socksAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer socksConn.Close()

	// SOCKS5 auth negotiation
	socksConn.Write([]byte{0x05, 0x01, 0x00})
	authReply := make([]byte, 2)
	io.ReadFull(socksConn, authReply)
	if authReply[1] != 0x00 {
		t.Fatalf("socks auth failed: %x", authReply)
	}

	// SOCKS5 CONNECT
	var port uint16
	fmt.Sscanf(targetPort, "%d", &port)
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(targetHost))}
	req = append(req, []byte(targetHost)...)
	req = append(req, byte(port>>8), byte(port&0xff))
	socksConn.Write(req)

	connReply := make([]byte, 10)
	io.ReadFull(socksConn, connReply)
	if connReply[1] != 0x00 {
		t.Fatalf("socks connect failed: rep=%d", connReply[1])
	}

	// 5. Send HTTP request through the tunnel
	httpReq := "GET / HTTP/1.0\r\nHost: test\r\n\r\n"
	socksConn.Write([]byte(httpReq))

	resp, err := io.ReadAll(socksConn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if string(resp) != targetResponse {
		t.Fatalf("response mismatch:\ngot:  %q\nwant: %q", resp, targetResponse)
	}
}

func TestE2EConcurrent(t *testing.T) {
	// Same as E2E but with 20 concurrent connections
	targetLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer targetLn.Close()
	targetAddr := targetLn.Addr().String()

	go func() {
		for {
			conn, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 1024)
				conn.Read(buf)
				conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nconcur!"))
			}()
		}
	}()

	nvSrv, _ := NewServer("127.0.0.1:0")
	defer nvSrv.Close()
	nvSrvAddr := nvSrv.Listener.Addr().String()
	proto := protocol.NewServer()

	go func() {
		for {
			conn, err := nvSrv.Accept(context.Background())
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := proto.HandleConnection(context.Background(), conn)
				if err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", req.Address(), 5*time.Second)
				if err != nil {
					protocol.SendACK(conn, protocol.StatusUnreachable)
					return
				}
				defer target.Close()
				protocol.SendACK(conn, protocol.StatusOK)
				proxy.Relay(conn, target)
			}()
		}
	}()

	socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer socksLn.Close()
	socksAddr := socksLn.Addr().String()
	transport := &Client{ServerAddr: nvSrvAddr}
	clientProto := protocol.NewClient()

	go func() {
		for {
			conn, err := socksLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				target, err := proxy.SOCKS5Handshake(conn)
				if err != nil {
					return
				}
				var sid [16]byte
				tConn, _ := transport.Dial(context.Background(), sid)
				if tConn == nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				defer tConn.Close()
				req := &protocol.Request{Command: protocol.CmdConnect, Host: target.Host, Port: target.Port}
				if clientProto.Handshake(context.Background(), tConn, req) != nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				proxy.SOCKS5SendSuccess(conn)
				proxy.Relay(conn, tConn)
			}()
		}
	}()

	targetHost, targetPort, _ := net.SplitHostPort(targetAddr)
	var port uint16
	fmt.Sscanf(targetPort, "%d", &port)

	var wg sync.WaitGroup
	errors := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", socksAddr, 5*time.Second)
			if err != nil {
				errors <- err
				return
			}
			defer conn.Close()

			conn.Write([]byte{0x05, 0x01, 0x00})
			reply := make([]byte, 2)
			io.ReadFull(conn, reply)

			req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(targetHost))}
			req = append(req, []byte(targetHost)...)
			req = append(req, byte(port>>8), byte(port&0xff))
			conn.Write(req)

			connReply := make([]byte, 10)
			io.ReadFull(conn, connReply)

			conn.Write([]byte("GET / HTTP/1.0\r\nHost: test\r\n\r\n"))
			resp, _ := io.ReadAll(conn)
			if string(resp) != "HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nconcur!" {
				errors <- fmt.Errorf("bad response: %q", resp)
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent error: %v", err)
	}
}

