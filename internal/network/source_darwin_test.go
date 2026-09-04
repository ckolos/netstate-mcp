// Compiled only on macOS (the _darwin_test.go suffix). These are real-host
// integration tests for the BSD-backed source; they run only on macOS.
package network

import (
	"context"
	"io"
	"net"
	"testing"
)

// TestDarwinSourceLoopback checks that the loopback interface "lo0" is present.
func TestDarwinSourceLoopback(t *testing.T) {
	ifaces, err := newDarwinSource().Interfaces(context.Background())
	if err != nil {
		t.Fatalf("darwinSource.Interfaces returned error: %v", err)
	}

	var found bool
	for _, iface := range ifaces {
		if iface.Name == "lo0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("macOS did not report loopback 'lo0' (got %d interfaces)", len(ifaces))
	}
}

// TestDarwinSourceStatsLoopback checks that statistics for "lo0" are retrievable.
func TestDarwinSourceStatsLoopback(t *testing.T) {
	stats, err := newDarwinSource().InterfaceStats(context.Background())
	if err != nil {
		t.Fatalf("darwinSource.InterfaceStats returned error: %v", err)
	}

	var found bool
	for _, s := range stats {
		if s.Name == "lo0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("macOS did not report statistics for 'lo0' (got %d entries)", len(stats))
	}
}

// TestDarwinSourceStatsCounters guards the if_msghdr2 field offsets. It moves
// a known amount of data over the loopback interface with a local TCP
// exchange, then requires lo0's RX/TX packet and byte counters to reflect at
// least that traffic. If the parser reads from wrong offsets (for example
// inside the message header, which is what an unadjusted nested
// unsafe.Offsetof does), those fields come back zero or garbage and this test
// fails. Counters are global and monotonic, so we assert lower bounds only.
func TestDarwinSourceStatsCounters(t *testing.T) {
	const payload = 8192

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialing loopback listener: %v", err)
	}
	if _, err := conn.Write(make([]byte, payload)); err != nil {
		t.Fatalf("writing loopback payload: %v", err)
	}
	conn.Close()
	<-done

	stats, err := newDarwinSource().InterfaceStats(context.Background())
	if err != nil {
		t.Fatalf("darwinSource.InterfaceStats returned error: %v", err)
	}

	var lo0 *InterfaceStatistics
	for i := range stats {
		if stats[i].Name == "lo0" {
			lo0 = &stats[i]
			break
		}
	}
	if lo0 == nil {
		t.Fatalf("no statistics entry for 'lo0' after loopback traffic")
	}
	if lo0.TxPackets == 0 || lo0.RxPackets == 0 {
		t.Errorf("lo0 packet counters did not move: %+v", *lo0)
	}
	if lo0.TxBytes < payload || lo0.RxBytes < payload {
		t.Errorf("lo0 byte counters below loopback payload (%d): %+v", payload, *lo0)
	}
}

// TestDarwinSourceDefaultRoutes checks that the query succeeds and that any
// returned route has a known family. It does not require a default route to
// exist (a machine may have none).
func TestDarwinSourceDefaultRoutes(t *testing.T) {
	routes, err := newDarwinSource().DefaultRoutes(context.Background(), AddressFamilyAll)
	if err != nil {
		t.Fatalf("darwinSource.DefaultRoutes returned error: %v", err)
	}

	for _, r := range routes {
		if r.Family != "ipv4" && r.Family != "ipv6" {
			t.Fatalf("default route has unexpected family %q: %+v", r.Family, r)
		}
	}
}
