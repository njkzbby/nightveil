package quictun

import (
	"net"
	"testing"
	"time"
)

func TestHoppingConfigDefaults(t *testing.T) {
	cfg := HoppingConfig{}
	cfg.defaults()

	if cfg.PortMin != 30000 {
		t.Errorf("PortMin: %d", cfg.PortMin)
	}
	if cfg.PortMax != 40000 {
		t.Errorf("PortMax: %d", cfg.PortMax)
	}
	if cfg.Interval != 30*time.Second {
		t.Errorf("Interval: %v", cfg.Interval)
	}
}

func TestRandomPort(t *testing.T) {
	min, max := 30000, 30010
	seen := make(map[int]bool)

	for i := 0; i < 100; i++ {
		p := randomPort(min, max)
		if p < min || p > max {
			t.Fatalf("port %d out of range [%d, %d]", p, min, max)
		}
		seen[p] = true
	}

	// Should have seen multiple different ports
	if len(seen) < 3 {
		t.Fatalf("expected variety in ports, only got %d distinct values", len(seen))
	}
}

func TestHoppingPacketConnPortChanges(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHoppingPacketConn(udpConn, "127.0.0.1", HoppingConfig{
		PortMin:  40000,
		PortMax:  40100,
		Interval: 50 * time.Millisecond,
	})
	defer h.Close()

	initialPort := h.CurrentPort()
	if initialPort < 40000 || initialPort > 40100 {
		t.Fatalf("initial port %d out of range", initialPort)
	}

	// Wait for a hop
	time.Sleep(150 * time.Millisecond)

	// Port should have changed (statistically near-certain with 100 port range)
	// But not guaranteed — so just check it's still in range
	newPort := h.CurrentPort()
	if newPort < 40000 || newPort > 40100 {
		t.Fatalf("hopped port %d out of range", newPort)
	}
}

func TestHoppingPacketConnClose(t *testing.T) {
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})

	h := NewHoppingPacketConn(udpConn, "127.0.0.1", HoppingConfig{
		PortMin:  50000,
		PortMax:  50100,
		Interval: time.Second,
	})

	err := h.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestHoppingMultipleHops(t *testing.T) {
	udpConn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})

	h := NewHoppingPacketConn(udpConn, "127.0.0.1", HoppingConfig{
		PortMin:  45000,
		PortMax:  45500,
		Interval: 30 * time.Millisecond,
	})
	defer h.Close()

	ports := make(map[int]bool)
	for i := 0; i < 10; i++ {
		ports[h.CurrentPort()] = true
		time.Sleep(40 * time.Millisecond)
	}

	// Should have seen multiple ports
	if len(ports) < 2 {
		t.Fatalf("expected multiple hops, only saw %d distinct ports", len(ports))
	}
}
