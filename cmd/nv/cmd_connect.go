package main

import (
	"context"
	"crypto/rand"
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
	"github.com/nightveil/nv/internal/protocol"
	"github.com/nightveil/nv/internal/proxy"
	"github.com/nightveil/nv/internal/security"
	"github.com/nightveil/nv/internal/throttle"
	"github.com/nightveil/nv/internal/transport/xhttp"
)

func runConnect() {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	configPath := fs.String("config", "", "path to client config")
	fs.Parse(os.Args[1:])

	var cfg config.ClientConfig

	// Check for URI as first positional arg
	uri := ""
	if fs.NArg() > 0 {
		arg := fs.Arg(0)
		if len(arg) > 12 && arg[:12] == "nightveil://" {
			uri = arg
		} else if *configPath == "" {
			*configPath = arg
		}
	}

	if uri != "" {
		parsed, remark, err := config.ParseURI(uri)
		if err != nil {
			fmt.Fprintf(os.Stderr, "URI error: %v\n", err)
			os.Exit(1)
		}
		cfg = *parsed
		log.Printf("imported: %s", remark)
	} else if *configPath != "" {
		if err := config.Load(*configPath, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "usage: nv connect \"nightveil://...\" or nv connect -config client.yaml\n")
		os.Exit(1)
	}

	c := cfg.Client

	// Auth
	serverPubKey, err := auth.DecodeKey(c.Auth.ServerPublicKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid key: %v\n", err)
		os.Exit(1)
	}
	shortID, err := hex.DecodeString(c.Auth.ShortID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid short ID: %v\n", err)
		os.Exit(1)
	}
	// Per-user key: if configured, use it. Otherwise generate ephemeral (legacy).
	var userPriv, userPub [32]byte
	if c.Auth.UserPrivateKey != "" {
		userPriv, err = auth.DecodeKey(c.Auth.UserPrivateKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid user private key: %v\n", err)
			os.Exit(1)
		}
		userPub, _ = auth.DerivePublicKey(userPriv)
	} else {
		userPriv, userPub, _ = auth.GenerateUserKeypair()
	}

	clientAuth := &auth.ClientX25519{
		ServerPublicKey: serverPubKey,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         shortID,
	}

	// Throttle
	detector := throttle.NewDetector(throttle.DetectorConfig{
		RTTSpikeMs:        c.AntiThrottle.DetectRTTSpikeMs,
		ThroughputDropPct: c.AntiThrottle.DetectThroughputDrop,
		ConfirmCount:      3,
	})
	adaptive := throttle.NewAdaptive(throttle.AdaptiveConfig{
		BaseParallelConns: 1,
		MaxParallelConns:  8,
		Strategy:          throttle.Strategy(c.AntiThrottle.Response),
	}, detector)

	// Rotator
	rotator := throttle.NewRotator(
		throttle.RotatorConfig{
			ParamRotateInterval:   5 * time.Minute,
			SessionRotateInterval: 30 * time.Minute,
			EmergencyRotate:       true,
		},
		throttle.DefaultRanges(),
		adaptive,
	)
	rotator.SetInitialParams(throttle.LiveParams{
		PathPrefix:   c.Transport.PathPrefix,
		UploadPath:   c.Transport.UploadPath,
		DownloadPath: c.Transport.DownloadPath,
		SessionKey:   c.Transport.SessionKeyName,
		ChunkSize:    c.Transport.MaxChunkSize,
	})

	// HTTP client
	var httpClient *http.Client
	serverURL := "http://" + c.Server.Address
	if c.TLS.Fingerprint != "" {
		sni := c.TLS.SNI
		if sni == "" {
			sni = c.Server.Address
			if h, _, err := net.SplitHostPort(sni); err == nil {
				sni = h
			}
		}
		httpClient = security.NewUTLSHTTPClient(security.UTLSConfig{
			ServerName:  sni,
			Fingerprint: c.TLS.Fingerprint,
			SkipVerify:  c.TLS.SkipVerify,
		})
		serverURL = "https://" + c.Server.Address
	} else {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	proto := protocol.NewClient()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rotator.Start()
	defer rotator.Stop()

	// Canary
	canary := throttle.NewCanary(throttle.CanaryConfig{
		Interval: 30 * time.Second, Timeout: 10 * time.Second, FailThreshold: 3,
	}, httpClient, detector)
	canary.OnStateChange(func(state throttle.CanaryState) {
		if state == throttle.CanaryBlocked {
			log.Printf("[!] TSPU interference detected — emergency rotation")
			rotator.EmergencyRotateNow()
		}
	})
	canary.Start()
	defer canary.Stop()

	detector.OnStateChange(func(state throttle.State) {
		log.Printf("[throttle] %s (parallel: %d)", state, adaptive.ParallelConns())
	})

	// Listener
	ln, err := net.Listen("tcp", c.Inbound.Listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Printf("\n  nightveil v%s connected\n", version)
	fmt.Printf("  %-18s %s\n", "SOCKS5 proxy:", c.Inbound.Listen)
	fmt.Printf("  %-18s %s\n", "Server:", c.Server.Address)
	fmt.Printf("  %-18s %s\n", "Anti-throttle:", "enabled")
	fmt.Printf("  %-18s %s\n", "Canary:", "active")
	fmt.Printf("  %-18s %s\n", "Param rotation:", "5min / 30min")
	fmt.Println()
	fmt.Printf("  Configure your browser proxy to %s (SOCKS5)\n", c.Inbound.Listen)
	fmt.Printf("  Or use V2RayN → Custom Config → point to nv.exe\n")
	fmt.Println()

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Println("\n  disconnected")
		cancel()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go handleSOCKS5Client(ctx, conn, serverURL, clientAuth, httpClient, proto, detector, rotator)
	}
}

func handleSOCKS5Client(
	ctx context.Context,
	socksConn net.Conn,
	serverURL string,
	clientAuth auth.ClientAuth,
	httpClient *http.Client,
	proto *protocol.Client,
	detector *throttle.Detector,
	rotator *throttle.Rotator,
) {
	defer socksConn.Close()

	target, err := proxy.SOCKS5Handshake(socksConn)
	if err != nil {
		return
	}

	params := rotator.GetParams()
	xhttpCfg := xhttp.Config{
		PathPrefix:     params.PathPrefix,
		UploadPath:     params.UploadPath,
		DownloadPath:   params.DownloadPath,
		SessionKeyName: params.SessionKey,
		MaxChunkSize:   params.ChunkSize,
	}
	xhttpClient := xhttp.NewClient(serverURL, xhttpCfg, clientAuth, httpClient)

	var sessionID [16]byte
	rand.Read(sessionID[:])

	dialStart := time.Now()
	tunnelConn, err := xhttpClient.Dial(ctx, sessionID)
	dialRTT := time.Since(dialStart)
	if err != nil {
		proxy.SOCKS5SendFailure(socksConn)
		return
	}
	defer tunnelConn.Close()

	detector.RecordRTT(dialRTT)

	req := &protocol.Request{
		Command: protocol.CmdConnect,
		Host:    target.Host,
		Port:    target.Port,
	}
	if err := proto.Handshake(ctx, tunnelConn, req); err != nil {
		proxy.SOCKS5SendFailure(socksConn)
		return
	}

	proxy.SOCKS5SendSuccess(socksConn)

	stats := proxy.RelayWithStats(socksConn, tunnelConn)
	if stats.Duration > time.Second {
		if tput := stats.ThroughputRecv(); tput > 0 {
			detector.RecordThroughput(tput)
		}
	}
}
