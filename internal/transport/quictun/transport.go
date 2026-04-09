package quictun

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/njkzbby/nightveil/internal/transport"
)

// Config for QUIC transport.
type Config struct {
	PSK        string // packet obfuscation key. Empty = no obfuscation.
	ListenAddr string // server listen address, e.g. "0.0.0.0:34000"
}

// --- Client ---

// Client implements transport.ClientTransport over QUIC.
type Client struct {
	ServerAddr string
	PSK        []byte
	TLSConfig  *tls.Config
}

func (c *Client) Dial(ctx context.Context, sessionID [16]byte) (transport.Conn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", c.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", c.ServerAddr, err)
	}

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	var pconn net.PacketConn = udpConn
	if len(c.PSK) > 0 {
		pconn = NewObfuscatedPacketConn(udpConn, c.PSK)
	}

	tlsCfg := c.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"nightveil"},
		}
	}

	quicConn, err := quic.Dial(ctx, pconn, udpAddr, tlsCfg, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
	})
	if err != nil {
		pconn.Close()
		return nil, fmt.Errorf("quic dial: %w", err)
	}

	stream, err := quicConn.OpenStreamSync(ctx)
	if err != nil {
		quicConn.CloseWithError(1, "stream open failed")
		pconn.Close()
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return &quicStreamConn{
		stream:   stream,
		quicConn: quicConn,
		pconn:    pconn,
	}, nil
}

func (c *Client) Close() error { return nil }

// --- Server ---

// Server implements transport.ServerTransport over QUIC.
type Server struct {
	listener *quic.Listener
	pconn    net.PacketConn
	acceptCh chan transport.Conn
}

func NewServer(cfg Config, tlsCert tls.Certificate) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", cfg.ListenAddr)
	if err != nil {
		return nil, err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen udp %s: %w", cfg.ListenAddr, err)
	}

	var pconn net.PacketConn = udpConn
	if cfg.PSK != "" {
		pconn = NewObfuscatedPacketConn(udpConn, []byte(cfg.PSK))
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"nightveil"},
	}

	listener, err := quic.Listen(pconn, tlsCfg, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
	})
	if err != nil {
		pconn.Close()
		return nil, fmt.Errorf("quic listen: %w", err)
	}

	s := &Server{
		listener: listener,
		pconn:    pconn,
		acceptCh: make(chan transport.Conn, 64),
	}
	go s.acceptLoop()

	return s, nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept(context.Background())
		if err != nil {
			close(s.acceptCh)
			return
		}
		go s.handleQUICConn(conn)
	}
}

func (s *Server) handleQUICConn(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		s.acceptCh <- &quicStreamConn{
			stream:   stream,
			quicConn: conn,
		}
	}
}

func (s *Server) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case conn, ok := <-s.acceptCh:
		if !ok {
			return nil, context.Canceled
		}
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) Close() error {
	s.listener.Close()
	return s.pconn.Close()
}

// --- quicStreamConn ---

type quicStreamConn struct {
	stream   *quic.Stream
	quicConn *quic.Conn
	pconn    net.PacketConn // owned by client, nil for server streams
}

func (c *quicStreamConn) Read(p []byte) (int, error)  { return c.stream.Read(p) }
func (c *quicStreamConn) Write(p []byte) (int, error) { return c.stream.Write(p) }
func (c *quicStreamConn) Close() error {
	c.stream.Close()
	if c.pconn != nil {
		c.pconn.Close()
	}
	return nil
}
func (c *quicStreamConn) LocalAddr() net.Addr  { return c.quicConn.LocalAddr() }
func (c *quicStreamConn) RemoteAddr() net.Addr { return c.quicConn.RemoteAddr() }
