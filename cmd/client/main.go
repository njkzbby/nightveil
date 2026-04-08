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
	listen := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	server := flag.String("server", "127.0.0.1:8443", "nightveil server address")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("nightveil client: SOCKS5 on %s → server %s (raw TCP mode)", *listen, *server)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")
		cancel()
		ln.Close()
	}()

	transport := &raw.Client{ServerAddr: *server}
	proto := protocol.NewClient()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("accept error: %v", err)
			continue
		}

		go handleSOCKS5(ctx, conn, transport, proto)
	}
}

func handleSOCKS5(ctx context.Context, socksConn net.Conn, transport *raw.Client, proto *protocol.Client) {
	defer socksConn.Close()

	// SOCKS5 handshake
	target, err := proxy.SOCKS5Handshake(socksConn)
	if err != nil {
		log.Printf("[%s] socks5 error: %v", socksConn.RemoteAddr(), err)
		return
	}

	log.Printf("[%s] CONNECT → %s:%d", socksConn.RemoteAddr(), target.Host, target.Port)

	// Dial tunnel to server
	var sessionID [16]byte // will be random in production; raw mode doesn't use auth
	tunnelConn, err := transport.Dial(ctx, sessionID)
	if err != nil {
		log.Printf("tunnel dial failed: %v", err)
		proxy.SOCKS5SendFailure(socksConn)
		return
	}
	defer tunnelConn.Close()

	// Protocol handshake
	req := &protocol.Request{
		Command: protocol.CmdConnect,
		Host:    target.Host,
		Port:    target.Port,
	}
	if err := proto.Handshake(ctx, tunnelConn, req); err != nil {
		log.Printf("protocol handshake failed: %v", err)
		proxy.SOCKS5SendFailure(socksConn)
		return
	}

	// SOCKS5 success
	if err := proxy.SOCKS5SendSuccess(socksConn); err != nil {
		log.Printf("socks5 reply failed: %v", err)
		return
	}

	// Relay: browser <-> tunnel
	proxy.Relay(socksConn, tunnelConn)
}
