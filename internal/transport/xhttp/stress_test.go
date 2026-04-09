package xhttp

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nightveil/nv/internal/crypto/auth"
	"github.com/nightveil/nv/internal/protocol"
	"github.com/nightveil/nv/internal/proxy"
	"github.com/nightveil/nv/internal/transport"
	"golang.org/x/crypto/curve25519"
)

func setupFullStack(t *testing.T) (
	*Server,                  // xhttp server
	auth.ClientAuth,          // client auth
	*httptest.Server,         // HTTP test server
	context.Context,
	context.CancelFunc,
) {
	t.Helper()

	var privKey [32]byte
	rand.Read(privKey[:])
	pubBytes, _ := curve25519.X25519(privKey[:], curve25519.Basepoint)
	var pubKey [32]byte
	copy(pubKey[:], pubBytes)

	userPriv, userPub, _ := auth.GenerateUserKeypair()

	serverAuth := &auth.ServerX25519{
		PrivateKey: privKey,
		Users: map[string]*auth.UserEntry{
			"aabb": {PublicKey: userPub, ShortID: "aabb"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	clientAuth := &auth.ClientX25519{
		ServerPublicKey: pubKey,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         []byte{0xAA, 0xBB},
	}

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	})

	srv := NewServer(cfg, serverAuth, fallback)
	ts := httptest.NewServer(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	return srv, clientAuth, ts, ctx, cancel
}

// TestStressFullE2E — full stack stress: SOCKS5 → protocol → XHTTP → target
// Simulates 30 concurrent connections each sending/receiving data.
func TestStressFullE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test — run with -short=false")
	}

	// 1. Target HTTP server (what we're proxying to)
	targetLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer targetLn.Close()
	go func() {
		for {
			c, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				c.Read(buf)
				c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nstress!"))
			}()
		}
	}()

	// 2. XHTTP server
	srv, clientAuth, ts, ctx, cancel := setupFullStack(t)
	defer cancel()
	defer ts.Close()

	proto := protocol.NewServer()

	go func() {
		for {
			conn, err := srv.Accept(ctx)
			if err != nil {
				return
			}
			go func(c transport.Conn) {
				defer c.Close()
				req, err := proto.HandleConnection(ctx, c)
				if err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", req.Address(), 5*time.Second)
				if err != nil {
					protocol.SendACK(c, protocol.StatusUnreachable)
					return
				}
				defer target.Close()
				protocol.SendACK(c, protocol.StatusOK)
				proxy.Relay(c, target)
			}(conn)
		}
	}()

	// 3. Client — SOCKS5 listener
	socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer socksLn.Close()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
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
				xhttpClient := NewClient(ts.URL, cfg, clientAuth, ts.Client())
				var sid [16]byte
				rand.Read(sid[:])
				tConn, err := xhttpClient.Dial(ctx, sid)
				if err != nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				defer tConn.Close()
				req := &protocol.Request{Command: protocol.CmdConnect, Host: target.Host, Port: target.Port}
				if clientProto.Handshake(ctx, tConn, req) != nil {
					proxy.SOCKS5SendFailure(conn)
					return
				}
				proxy.SOCKS5SendSuccess(conn)
				proxy.Relay(conn, tConn)
			}()
		}
	}()

	// 4. Stress: 30 concurrent SOCKS5 clients
	targetHost, targetPort, _ := net.SplitHostPort(targetLn.Addr().String())
	var port uint16
	fmt.Sscanf(targetPort, "%d", &port)

	numClients := 30
	var wg sync.WaitGroup
	var errors atomic.Int32
	var successes atomic.Int32

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", socksLn.Addr().String(), 5*time.Second)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()

			// SOCKS5 handshake
			conn.Write([]byte{0x05, 0x01, 0x00})
			reply := make([]byte, 2)
			io.ReadFull(conn, reply)

			req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(targetHost))}
			req = append(req, []byte(targetHost)...)
			req = append(req, byte(port>>8), byte(port&0xff))
			conn.Write(req)

			connReply := make([]byte, 10)
			io.ReadFull(conn, connReply)

			if connReply[1] != 0x00 {
				errors.Add(1)
				return
			}

			// HTTP request through tunnel
			conn.Write([]byte("GET / HTTP/1.0\r\nHost: test\r\n\r\n"))
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, _ := io.ReadAll(conn)

			if len(resp) > 0 && string(resp[len(resp)-7:]) == "stress!" {
				successes.Add(1)
			} else {
				errors.Add(1)
			}
		}(i)
	}

	wg.Wait()

	s := successes.Load()
	e := errors.Load()
	t.Logf("Results: %d/%d succeeded, %d errors", s, numClients, e)

	if s < int32(numClients)*80/100 {
		t.Fatalf("too many failures: %d/%d succeeded", s, numClients)
	}
}

// TestStressStreamingParallel — multiple parallel long-lived streaming connections
func TestStressStreamingParallel(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupFullStack(t)
	defer cancel()
	defer ts.Close()

	numStreams := 10
	tokensPerStream := 20

	// Server: accept and echo
	go func() {
		for {
			conn, err := srv.Accept(ctx)
			if err != nil {
				return
			}
			go func(c transport.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	cfg := Config{MaxChunkSize: 1024, SessionTimeout: 30}

	var wg sync.WaitGroup
	var totalTokens atomic.Int32

	for s := 0; s < numStreams; s++ {
		wg.Add(1)
		go func(streamID int) {
			defer wg.Done()

			client := NewClient(ts.URL, cfg, clientAuth, ts.Client())
			var sid [16]byte
			rand.Read(sid[:])

			conn, err := client.Dial(ctx, sid)
			if err != nil {
				return
			}
			defer conn.Close()

			time.Sleep(100 * time.Millisecond)

			for tok := 0; tok < tokensPerStream; tok++ {
				msg := fmt.Sprintf("s%d_t%d", streamID, tok)
				conn.Write([]byte(msg))

				buf := make([]byte, 100)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				if string(buf[:n]) == msg {
					totalTokens.Add(1)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}(s)
	}

	wg.Wait()

	expected := int32(numStreams * tokensPerStream)
	got := totalTokens.Load()
	t.Logf("Streaming: %d/%d tokens round-tripped", got, expected)

	if got < expected*70/100 {
		t.Fatalf("too many lost tokens: %d/%d", got, expected)
	}
}
