package proxy

import (
	"net"
	"testing"
	"time"
)

func TestRelayWithStats(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	go func() {
		data := make([]byte, 1000)
		leftClient.Write(data)
		time.Sleep(10 * time.Millisecond)
		leftClient.Close()
	}()

	go func() {
		data := make([]byte, 500)
		rightClient.Write(data)
		time.Sleep(10 * time.Millisecond)
		rightClient.Close()
	}()

	stats := RelayWithStats(leftServer, rightServer)

	if stats.BytesSent.Load() != 1000 {
		t.Errorf("bytes sent: got %d, want 1000", stats.BytesSent.Load())
	}
	if stats.BytesRecv.Load() != 500 {
		t.Errorf("bytes recv: got %d, want 500", stats.BytesRecv.Load())
	}
}

func TestRelayWithStatsThroughput(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	size := 100000
	go func() {
		data := make([]byte, size)
		for i := 0; i < len(data); i += 1024 {
			end := i + 1024
			if end > len(data) {
				end = len(data)
			}
			leftClient.Write(data[i:end])
			time.Sleep(time.Millisecond) // slow down to ensure measurable duration
		}
		leftClient.Close()
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := rightClient.Read(buf)
			if err != nil {
				break
			}
		}
		rightClient.Close()
	}()

	stats := RelayWithStats(leftServer, rightServer)

	if stats.BytesSent.Load() < int64(size) {
		t.Errorf("bytes sent: got %d, want >= %d", stats.BytesSent.Load(), size)
	}
	if stats.Duration <= 0 {
		t.Error("duration should be positive")
	}
}

func TestRelayWithStatsZeroDuration(t *testing.T) {
	stats := &RelayStats{}
	if stats.ThroughputSend() != 0 {
		t.Error("zero duration should return 0 throughput")
	}
	if stats.ThroughputRecv() != 0 {
		t.Error("zero duration should return 0 throughput")
	}
}

func TestRelayWithStatsTimestamp(t *testing.T) {
	leftClient, leftServer := net.Pipe()
	rightClient, rightServer := net.Pipe()

	before := time.Now()
	go func() {
		leftClient.Write([]byte("x"))
		leftClient.Close()
	}()
	go func() {
		rightClient.Close()
	}()

	stats := RelayWithStats(leftServer, rightServer)

	if stats.StartTime.Before(before) {
		t.Error("start time should be after test start")
	}
}
