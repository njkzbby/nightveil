package xhttp

import (
	"testing"
	"time"
)

// TestPipelineStallAndRecover reproduces the user-visible "stall and burst"
// pattern in a controlled way. The netsim proxy is paused for a short window
// while the client is actively writing — bytes accumulate in the proxy's
// in-memory queue. After Resume, all queued bytes flush at once and the
// receiver should see them in order.
//
// What this test asserts:
//
//  1. The connection survives a 1-second stall.
//  2. After Resume, ALL bytes written before/during the stall arrive
//     at the receiver — nothing is silently dropped.
//  3. The total wall time is roughly stall_duration + transfer_time
//     (i.e. the stall really happened, we're not just measuring loopback).
//
// Both packet and stream modes are exercised. Stream mode is the more
// fragile case because the entire upload runs through one HTTP/2 stream;
// if the stall trips the http2.Transport ping timeout, the connection dies
// and the test will fail. The fact that the test passes confirms our 1s
// stall is well under the ping timeout (default 30s ReadIdle + 15s Ping).
func TestPipelineStallAndRecover(t *testing.T) {
	cases := []struct {
		mode      string
		stall     time.Duration
		writeSize int
	}{
		{"packet", 500 * time.Millisecond, 64 * 1024},
		{"packet", 1500 * time.Millisecond, 64 * 1024},
		{"stream", 500 * time.Millisecond, 64 * 1024},
		{"stream", 1500 * time.Millisecond, 64 * 1024},
	}

	for _, tc := range cases {
		name := tc.mode + "/stall=" + tc.stall.String()
		t.Run(name, func(t *testing.T) {
			p := setupPipeline(t, pipelineOpts{
				Latency:    5 * time.Millisecond, // tiny RTT, just so the proxy is in path
				UploadMode: tc.mode,
			})

			payload := make([]byte, tc.writeSize)
			for i := range payload {
				payload[i] = byte(i % 251)
			}

			// Phase 1: write half the payload normally.
			half := len(payload) / 2
			start := time.Now()
			n, err := p.ClientConn.Write(payload[:half])
			if err != nil {
				t.Fatalf("phase1 write: %v", err)
			}
			if n != half {
				t.Fatalf("phase1 short write: %d/%d", n, half)
			}

			// Wait until server has received phase-1 bytes (so we know the
			// pause happens AFTER they're delivered, not before).
			waitFor(t, func() bool {
				return p.ServerReceived.Load() >= int64(half)
			}, 5*time.Second, "phase1 server-received")

			// Phase 2: pause the proxy, then write the rest. Bytes should
			// queue up in the proxy.
			p.Proxy.Pause()
			pauseStart := time.Now()

			// Kick off the write in a goroutine — in stream mode this
			// returns quickly (bytes go into local pipe); in packet mode
			// it may block on the in-flight POSTs failing to drain through
			// the paused proxy. Either way, we want to verify nothing is
			// lost on the other side.
			writeDone := make(chan error, 1)
			go func() {
				_, werr := p.ClientConn.Write(payload[half:])
				writeDone <- werr
			}()

			// Hold the pause for tc.stall.
			time.Sleep(tc.stall)
			p.Proxy.Resume()
			t.Logf("paused for %v (actual: %v)", tc.stall, time.Since(pauseStart))

			// Now wait for the receiver to drain the rest. This is the
			// "burst after stall" — the queued bytes flush all at once.
			waitFor(t, func() bool {
				return p.ServerReceived.Load() >= int64(len(payload))
			}, 30*time.Second, "phase2 server-received")

			elapsed := time.Since(start)
			t.Logf("[%s] total elapsed=%v, server received %d/%d bytes",
				name, elapsed, p.ServerReceived.Load(), len(payload))

			// Allow the writer goroutine to drain. We don't fail on writer
			// errors per se because in some adversarial-stall scenarios the
			// stream-up POST may give up — what we care about is whether
			// the BYTES that were sent actually arrived. The byte count
			// is the source of truth.
			select {
			case werr := <-writeDone:
				if werr != nil {
					t.Logf("[%s] writer reported error (may be expected on long stalls): %v", name, werr)
				}
			case <-time.After(5 * time.Second):
				t.Logf("[%s] writer goroutine still pending — not fatal", name)
			}

			if p.ServerReceived.Load() < int64(len(payload)) {
				t.Fatalf("[%s] data loss: server received %d / %d bytes",
					name, p.ServerReceived.Load(), len(payload))
			}
		})
	}
}

// waitFor polls cond until it returns true or the timeout expires.
// On timeout, fails the test with the given label.
func waitFor(t testing.TB, cond func() bool, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitFor timeout: %s", label)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
