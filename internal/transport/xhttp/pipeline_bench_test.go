package xhttp

import (
	"fmt"
	"testing"
	"time"
)

// TestPipelineUploadComparison runs the same upload payload through both
// modes at multiple RTTs and prints a comparison table. Useful as a real
// regression check that stream-up actually beats packet-up under WAN
// conditions.
//
// Run with: go test -run TestPipelineUploadComparison -v ./internal/transport/xhttp/
//
// Expected ballpark (loopback machine, your numbers will vary):
//
//	mode=packet RTT=  0ms  payload=4MiB  ~  900-1500 Mbps
//	mode=stream RTT=  0ms  payload=4MiB  ~ 1500-3000 Mbps
//	mode=packet RTT= 50ms  payload=4MiB  ~   15-25 Mbps  ← matches user-reported ceiling
//	mode=stream RTT= 50ms  payload=4MiB  ~  100-300 Mbps
//	mode=packet RTT=100ms  payload=4MiB  ~    8-12 Mbps
//	mode=stream RTT=100ms  payload=4MiB  ~   80-200 Mbps
func TestPipelineUploadComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("comparison test takes ~30s — run without -short")
	}

	cases := []struct {
		name    string
		latency time.Duration // one-way; RTT = 2x
		mode    string
	}{
		{"packet/0ms", 0, "packet"},
		{"stream/0ms", 0, "stream"},
		{"packet/RTT50ms", 25 * time.Millisecond, "packet"},
		{"stream/RTT50ms", 25 * time.Millisecond, "stream"},
		{"packet/RTT100ms", 50 * time.Millisecond, "packet"},
		{"stream/RTT100ms", 50 * time.Millisecond, "stream"},
	}

	const payloadSize = 1 * 1024 * 1024 // 1 MiB — small enough that even
	//                                     // packet-up at high RTT finishes quickly

	t.Log("")
	t.Log("=== Upload throughput: packet-up vs stream-up ===")
	t.Logf("%-15s %12s %12s %12s", "case", "elapsed", "Mbps", "MiB/s")
	t.Logf("%-15s %12s %12s %12s", "----", "-------", "----", "-----")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := setupPipeline(t, pipelineOpts{
				Latency:    tc.latency,
				UploadMode: tc.mode,
			})

			payload := make([]byte, payloadSize)
			for i := range payload {
				payload[i] = byte(i % 251)
			}

			elapsed, mbps := transferUpload(t, p, payload)
			mibps := float64(payloadSize) / elapsed.Seconds() / (1024 * 1024)

			t.Logf("%-15s %12v %12.2f %12.2f", tc.name, elapsed.Truncate(time.Millisecond), mbps, mibps)
		})
	}
}

// BenchmarkPipelineUpload — micro-benchmark form of the comparison above.
// Allocates one pipeline per b.N iteration. Useful for go test -bench.
//
// Example: go test -bench=BenchmarkPipelineUpload -benchtime=3x ./internal/transport/xhttp/
func BenchmarkPipelineUpload(b *testing.B) {
	cases := []struct {
		latency time.Duration
		mode    string
	}{
		{0, "packet"},
		{0, "stream"},
		{50 * time.Millisecond, "packet"},
		{50 * time.Millisecond, "stream"},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("%s_RTT%dms", tc.mode, int(tc.latency*2/time.Millisecond))
		b.Run(name, func(b *testing.B) {
			payload := make([]byte, 1*1024*1024)
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				p := setupPipeline(b, pipelineOpts{
					Latency:    tc.latency,
					UploadMode: tc.mode,
				})
				if _, err := p.ClientConn.Write(payload); err != nil {
					b.Fatalf("write: %v", err)
				}
				p.Close()
			}
		})
	}
}
