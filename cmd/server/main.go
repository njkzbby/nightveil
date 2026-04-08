package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/nightveil/nv/internal/protocol"
	"github.com/nightveil/nv/internal/proxy"
	"github.com/nightveil/nv/internal/transport/raw"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8443", "listen address")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("nightveil server starting on %s (raw TCP mode)", *listen)

	srv, err := raw.NewServer(*listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on signal
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")
		cancel()
		srv.Close()
	}()

	proto := protocol.NewServer()

	for {
		conn, err := srv.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("accept error: %v", err)
			continue
		}

		go handleConn(ctx, conn, proto)
	}
}

func handleConn(ctx context.Context, conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}, proto *protocol.Server) {
	defer conn.Close()

	req, err := proto.HandleConnection(ctx, conn)
	if err != nil {
		log.Printf("[%s] handshake error: %v", conn.RemoteAddr(), err)
		return
	}

	log.Printf("[%s] CONNECT → %s", conn.RemoteAddr(), req.Address())

	// Dial target
	target, err := net.DialTimeout("tcp", req.Address(), 10e9)
	if err != nil {
		log.Printf("[%s] dial %s failed: %v", conn.RemoteAddr(), req.Address(), err)
		protocol.SendACK(conn, protocol.StatusUnreachable)
		return
	}
	defer target.Close()

	// Send ACK
	if err := protocol.SendACK(conn, protocol.StatusOK); err != nil {
		log.Printf("[%s] send ACK failed: %v", conn.RemoteAddr(), err)
		return
	}

	// Relay
	if err := proxy.Relay(conn, target); err != nil {
		log.Printf("[%s] relay ended: %v", conn.RemoteAddr(), err)
	}
}
