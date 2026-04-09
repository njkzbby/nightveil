package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/nightveil/nv/internal/protocol"
)

const (
	udpBufSize    = 65535
	udpIdleTimeout = 60 * time.Second
)

// UDPRelayServer handles UDP relay on the server side.
// For each unique client session + destination, it opens a local UDP socket
// and forwards datagrams bidirectionally.
type UDPRelayServer struct {
	relay *protocol.UDPRelay

	mu    sync.Mutex
	conns map[string]*udpConn // key: "host:port"
}

type udpConn struct {
	conn     *net.UDPConn
	lastUsed time.Time
}

// NewUDPRelayServer creates a server-side UDP relay.
func NewUDPRelayServer(relay *protocol.UDPRelay) *UDPRelayServer {
	s := &UDPRelayServer{
		relay: relay,
		conns: make(map[string]*udpConn),
	}
	go s.cleanupLoop()
	return s
}

// Run reads UDP messages from the tunnel and forwards them to destinations.
// Blocks until the tunnel closes.
func (s *UDPRelayServer) Run() error {
	for {
		msg, err := s.relay.ReadMessage()
		if err != nil {
			return fmt.Errorf("read udp: %w", err)
		}

		destAddr := net.JoinHostPort(msg.Host, fmt.Sprintf("%d", msg.Port))

		// Get or create UDP socket for this destination
		uc, err := s.getOrCreate(destAddr)
		if err != nil {
			log.Printf("[udp] dial %s failed: %v", destAddr, err)
			continue
		}

		// Send to destination
		_, err = uc.conn.Write(msg.Payload)
		if err != nil {
			log.Printf("[udp] write to %s failed: %v", destAddr, err)
			continue
		}

		uc.lastUsed = time.Now()
	}
}

func (s *UDPRelayServer) getOrCreate(destAddr string) (*udpConn, error) {
	s.mu.Lock()
	if uc, ok := s.conns[destAddr]; ok {
		s.mu.Unlock()
		return uc, nil
	}
	s.mu.Unlock()

	// Resolve and dial
	addr, err := net.ResolveUDPAddr("udp", destAddr)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	uc := &udpConn{conn: conn, lastUsed: time.Now()}

	s.mu.Lock()
	s.conns[destAddr] = uc
	s.mu.Unlock()

	// Start reading responses from this destination
	go s.readFromDest(conn, destAddr)

	return uc, nil
}

// readFromDest reads UDP responses from destination and sends them back through tunnel.
func (s *UDPRelayServer) readFromDest(conn *net.UDPConn, destAddr string) {
	buf := make([]byte, udpBufSize)
	for {
		conn.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			// Timeout or closed — clean up
			s.mu.Lock()
			delete(s.conns, destAddr)
			s.mu.Unlock()
			conn.Close()
			return
		}

		// Send response back through tunnel
		msg := &protocol.UDPMessage{
			Host:    remoteAddr.IP.String(),
			Port:    uint16(remoteAddr.Port),
			Payload: buf[:n],
		}
		if err := s.relay.WriteMessage(msg); err != nil {
			return
		}
	}
}

func (s *UDPRelayServer) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for key, uc := range s.conns {
			if now.Sub(uc.lastUsed) > udpIdleTimeout {
				uc.conn.Close()
				delete(s.conns, key)
			}
		}
		s.mu.Unlock()
	}
}

// Close shuts down all UDP connections.
func (s *UDPRelayServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, uc := range s.conns {
		uc.conn.Close()
		delete(s.conns, key)
	}
}

// UDPRelayClient handles UDP relay on the client side.
// It listens on a local UDP port and forwards datagrams through the tunnel.
type UDPRelayClient struct {
	relay    *protocol.UDPRelay
	listener *net.UDPConn
	target   string // original destination requested by SOCKS5

	mu      sync.Mutex
	clients map[string]*net.UDPAddr // key: "local_addr" → client addr
}

// NewUDPRelayClient creates a client-side UDP relay.
func NewUDPRelayClient(relay *protocol.UDPRelay, listenAddr string, target string) (*UDPRelayClient, error) {
	addr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	return &UDPRelayClient{
		relay:    relay,
		listener: conn,
		target:   target,
		clients:  make(map[string]*net.UDPAddr),
	}, nil
}

// LocalAddr returns the local listening address.
func (c *UDPRelayClient) LocalAddr() net.Addr {
	return c.listener.LocalAddr()
}

// RunRead reads from local UDP clients and sends through tunnel.
func (c *UDPRelayClient) RunRead() error {
	buf := make([]byte, udpBufSize)
	for {
		n, clientAddr, err := c.listener.ReadFromUDP(buf)
		if err != nil {
			return err
		}

		// Remember client address for responses
		c.mu.Lock()
		c.clients[clientAddr.String()] = clientAddr
		c.mu.Unlock()

		// Parse target — for SOCKS5 UDP ASSOCIATE, the target is in the SOCKS5 UDP header
		// For simplicity, use the configured target
		host, port := splitHostPort(c.target)

		msg := &protocol.UDPMessage{
			Host:    host,
			Port:    port,
			Payload: buf[:n],
		}
		if err := c.relay.WriteMessage(msg); err != nil {
			return err
		}
	}
}

// RunWrite reads from tunnel and sends to local UDP clients.
func (c *UDPRelayClient) RunWrite() error {
	for {
		msg, err := c.relay.ReadMessage()
		if err != nil {
			return err
		}

		// Send to the most recent client
		c.mu.Lock()
		var clientAddr *net.UDPAddr
		for _, addr := range c.clients {
			clientAddr = addr
			break
		}
		c.mu.Unlock()

		if clientAddr != nil {
			c.listener.WriteToUDP(msg.Payload, clientAddr)
		}
	}
}

// Close shuts down the listener.
func (c *UDPRelayClient) Close() {
	c.listener.Close()
}

func splitHostPort(addr string) (string, uint16) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
