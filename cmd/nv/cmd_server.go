package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nightveil/nv/internal/config"
	"github.com/nightveil/nv/internal/crypto/auth"
	"github.com/nightveil/nv/internal/fallback"
	"github.com/nightveil/nv/internal/middleware"
	"github.com/nightveil/nv/internal/protocol"
	"github.com/nightveil/nv/internal/proxy"
	"github.com/nightveil/nv/internal/security"
	"github.com/nightveil/nv/internal/transport/xhttp"
)

func runServer() {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	configPath := fs.String("config", "server.yaml", "path to server config")
	fs.Parse(os.Args[1:])

	var cfg config.ServerConfig
	if err := config.Load(*configPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	s := cfg.Server

	privKey, err := auth.DecodeKey(s.Auth.PrivateKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid private key: %v\n", err)
		os.Exit(1)
	}

	// Build users map from config
	users := make(map[string]*auth.UserEntry)

	// Per-user keys (new format)
	for _, u := range s.Auth.Users {
		entry := &auth.UserEntry{ShortID: u.ShortID, Name: u.Name}
		if u.PublicKey != "" {
			key, err := auth.DecodeKey(u.PublicKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid public key for user %s: %v\n", u.Name, err)
				os.Exit(1)
			}
			entry.PublicKey = key
		}
		users[u.ShortID] = entry
	}

	// Legacy short_ids (backward-compatible)
	for _, id := range s.Auth.ShortIDs {
		if _, exists := users[id]; !exists {
			users[id] = &auth.UserEntry{ShortID: id, Name: id}
		}
	}

	maxTimeDiff := s.Auth.MaxTimeDiff
	if maxTimeDiff == 0 {
		maxTimeDiff = config.DefaultMaxTimeDiff
	}

	serverAuth := &auth.ServerX25519{
		PrivateKey:  privKey,
		Users:       users,
		MaxTimeDiff: maxTimeDiff,
		TokenHeader: config.DefaultTokenHeader,
	}

	// In REALITY mode, fallback reverse-proxies to dest
	fbMode := s.Fallback.Mode
	fbUpstream := s.Fallback.Upstream
	if s.TLS.Dest != "" && fbMode == "" {
		fbMode = "reverse_proxy"
		dest := security.FormatRealityDest(s.TLS.Dest)
		fbUpstream = "https://" + dest
	}
	fb, err := fallback.New(fbMode, fbUpstream, s.Fallback.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fallback error: %v\n", err)
		os.Exit(1)
	}

	xhttpCfg := xhttp.Config{
		MaxChunkSize:   s.Transport.MaxChunkSize,
		SessionTimeout: s.Transport.SessionTimeout,
	}
	xhttpSrv := xhttp.NewServer(xhttpCfg, serverAuth, fb)

	var chain middleware.Chain
	proto := protocol.NewServer()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			conn, err := xhttpSrv.Accept(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			go handleTunnel(ctx, conn, chain, proto)
		}
	}()

	httpServer := &http.Server{
		Addr:         s.Listen,
		Handler:      xhttpSrv,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	pubKey, _ := auth.DerivePublicKey(privKey)
	userCount := len(s.Auth.Users) + len(s.Auth.ShortIDs)

	if s.TLS.Dest != "" {
		// --- REALITY mode ---
		// Unauthenticated connections forwarded to real dest server.
		// Probes see real google.com (or whatever dest is).
		dest := security.FormatRealityDest(s.TLS.Dest)
		log.Printf("nightveil server v%s on %s (REALITY → %s)", version, s.Listen, dest)
		log.Printf("  public key: %s", hex.EncodeToString(pubKey[:]))
		log.Printf("  users: %d", userCount)

		// In REALITY mode, we don't use Go TLS. Instead:
		// 1. Accept raw TCP
		// 2. The XHTTP handler + fallback already handles auth routing
		// 3. Fallback serves real content (or we use RealityListener to forward)
		//
		// For XHTTP-over-TLS through CDN: CDN terminates TLS, we get plaintext.
		// For direct connections: client uses uTLS, server needs TLS cert.
		//
		// Hybrid approach: use TLS cert if available, otherwise plaintext.
		// REALITY dest is used as the fallback — any unauth request gets
		// reverse-proxied to dest.
		if s.TLS.CertFile != "" {
			tlsCfg, err := security.NewServerTLSConfig(s.TLS.CertFile, s.TLS.KeyFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "TLS error: %v\n", err)
				os.Exit(1)
			}
			httpServer.TLSConfig = tlsCfg
			go func() {
				if err := httpServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
					log.Fatalf("server error: %v", err)
				}
			}()
		} else {
			go func() {
				if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
					log.Fatalf("server error: %v", err)
				}
			}()
		}

	} else if s.TLS.CertFile != "" && s.TLS.KeyFile != "" {
		// --- TLS mode (with cert) ---
		tlsCfg, err := security.NewServerTLSConfig(s.TLS.CertFile, s.TLS.KeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "TLS error: %v\n", err)
			os.Exit(1)
		}
		httpServer.TLSConfig = tlsCfg

		log.Printf("nightveil server v%s on %s (TLS)", version, s.Listen)
		log.Printf("  public key: %s", hex.EncodeToString(pubKey[:]))
		log.Printf("  users: %d", userCount)

		go func() {
			if err := httpServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}()
	} else {
		// --- Plaintext mode (behind CDN/nginx) ---
		log.Printf("nightveil server v%s on %s (plaintext)", version, s.Listen)
		log.Printf("  public key: %s", hex.EncodeToString(pubKey[:]))
		log.Printf("  users: %d", userCount)

		go func() {
			if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
	cancel()
}

func handleTunnel(ctx context.Context, conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}, chain middleware.Chain, proto *protocol.Server) {
	wrapped := chain.Wrap(conn)
	defer wrapped.Close()

	req, err := proto.HandleConnection(ctx, wrapped)
	if err != nil {
		log.Printf("[%s] handshake error: %v", conn.RemoteAddr(), err)
		return
	}

	switch req.Command {
	case protocol.CmdConnect:
		log.Printf("[%s] TCP → %s", conn.RemoteAddr(), req.Address())
		target, err := net.DialTimeout("tcp", req.Address(), 10*time.Second)
		if err != nil {
			protocol.SendACK(wrapped, protocol.StatusUnreachable)
			return
		}
		defer target.Close()
		protocol.SendACK(wrapped, protocol.StatusOK)
		proxy.Relay(wrapped, target)

	case protocol.CmdUDP:
		log.Printf("[%s] UDP → %s", conn.RemoteAddr(), req.Address())
		protocol.SendACK(wrapped, protocol.StatusOK)
		relay := protocol.NewUDPRelay(wrapped)
		udpSrv := proxy.NewUDPRelayServer(relay)
		defer udpSrv.Close()
		udpSrv.Run()

	default:
		protocol.SendACK(wrapped, protocol.StatusRefused)
	}
}

// Silence unused import
var _ = tls.Config{}
