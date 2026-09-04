// Compiled only on Linux (the _linux_test.go suffix). These tests exercise the
// netlink-backed source and its normalization against a real host and a
// hand-built link.
package network

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// TestNormalizeLink is deterministic: it builds a netlink.Device by hand and
// checks the domain Interface it produces, without touching the host.
func TestNormalizeLink(t *testing.T) {
	link := &netlink.Device{
		LinkAttrs: netlink.LinkAttrs{
			Index:        7,
			MTU:          1500,
			Name:         "eth0",
			HardwareAddr: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01},
			Flags:        net.FlagUp | net.FlagBroadcast,
			OperState:    netlink.OperUp,
		},
	}

	got := normalizeLink(link)

	want := Interface{
		Name:      "eth0",
		Index:     7,
		MTU:       1500,
		MAC:       "de:ad:be:ef:00:01",
		Up:        true,
		OperState: "up",
	}
	if got != want {
		t.Fatalf("normalizeLink(...) = %+v; want %+v", got, want)
	}
}

// TestLinkStatistics is deterministic: it builds a netlink.Device with counters
// and checks the domain InterfaceStatistics it produces.
func TestLinkStatistics(t *testing.T) {
	link := &netlink.Device{
		LinkAttrs: netlink.LinkAttrs{
			Name: "eth0",
			Statistics: &netlink.LinkStatistics{
				RxBytes:   4096,
				RxPackets: 40,
				RxErrors:  1,
				RxDropped: 2,
				TxBytes:   2048,
				TxPackets: 20,
				TxErrors:  3,
				TxDropped: 4,
			},
		},
	}

	got := linkStatistics(link)

	want := InterfaceStatistics{
		Name:      "eth0",
		RxBytes:   4096,
		RxPackets: 40,
		RxErrors:  1,
		RxDropped: 2,
		TxBytes:   2048,
		TxPackets: 20,
		TxErrors:  3,
		TxDropped: 4,
	}
	if got != want {
		t.Fatalf("linkStatistics(...) = %+v; want %+v", got, want)
	}
}

// TestLinuxSourceLoopback is a real-host integration test: on Linux the
// loopback interface "lo" must always be present.
func TestLinuxSourceLoopback(t *testing.T) {
	ifaces, err := newLinuxSource().Interfaces(context.Background())
	if err != nil {
		t.Fatalf("linuxSource.Interfaces returned error: %v", err)
	}

	var found bool
	for _, iface := range ifaces {
		if iface.Name == "lo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("netlink did not report loopback 'lo' (got %d interfaces)", len(ifaces))
	}
}

// TestLinuxSourceStatsLoopback is a real-host integration test: statistics for
// the loopback interface must be retrievable.
func TestLinuxSourceStatsLoopback(t *testing.T) {
	stats, err := newLinuxSource().InterfaceStats(context.Background())
	if err != nil {
		t.Fatalf("linuxSource.InterfaceStats returned error: %v", err)
	}

	var found bool
	for _, s := range stats {
		if s.Name == "lo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("netlink did not report statistics for 'lo' (got %d entries)", len(stats))
	}
}

// TestLinuxSourceDefaultRoutes is a real-host integration test. It does not
// assert that a default route exists (a sandboxed netns may have none); it only
// checks that the query succeeds and that anything returned looks like a
// default route with a known family.
func TestLinuxSourceDefaultRoutes(t *testing.T) {
	routes, err := newLinuxSource().DefaultRoutes(context.Background(), AddressFamilyAll)
	if err != nil {
		t.Fatalf("linuxSource.DefaultRoutes returned error: %v", err)
	}

	for _, r := range routes {
		if r.Family != "ipv4" && r.Family != "ipv6" {
			t.Fatalf("default route has unexpected family %q: %+v", r.Family, r)
		}
	}
}

// TestIsDefaultRoute checks that a default route is detected both when netlink
// reports it as a nil Dst and when it reports the equivalent zero-length prefix
// (0.0.0.0/0). The latter is what kernels/netlink commonly return in practice;
// regressing to only the nil case drops every real default route.
func TestIsDefaultRoute(t *testing.T) {
	def := func(mask net.IPMask, ip net.IP) *net.IPNet {
		return &net.IPNet{IP: ip, Mask: mask}
	}
	cases := []struct {
		name string
		r    netlink.Route
		want bool
	}{
		{"nil dst (classic default)", netlink.Route{Dst: nil}, true},
		{"zero-length v4 prefix", netlink.Route{Dst: def(net.CIDRMask(0, 32), net.ParseIP("0.0.0.0"))}, true},
		{"zero-length v6 prefix", netlink.Route{Dst: def(net.CIDRMask(0, 128), net.ParseIP("::"))}, true},
		{"host route", netlink.Route{Dst: def(net.CIDRMask(32, 32), net.ParseIP("10.0.0.42"))}, false},
		{"subnet route", netlink.Route{Dst: def(net.CIDRMask(24, 32), net.ParseIP("10.0.0.0"))}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDefaultRoute(tc.r); got != tc.want {
				t.Fatalf("isDefaultRoute(%+v) = %v; want %v", tc.r, got, tc.want)
			}
		})
	}
}

// TestNormalizeRoute is deterministic: it builds a netlink.Route by hand and
// checks the domain Route it produces, without touching the host. It uses a
// link index that does not exist, so the interface lookup fails and Interface
// stays blank. Two routes identical in gateway/metric are still distinguished
// by their extra attributes (e.g. TCP window tuning), which surface in the
// generic Attr map.
//
// The IPs use the RFC 5737 documentation range (192.0.2.0/24) so tests carry no
// real network details.
func TestNormalizeRoute(t *testing.T) {
	cases := []struct {
		name string
		r    netlink.Route
		want Route
	}{
		{
			"plain default",
			netlink.Route{
				Family:    netlink.FAMILY_V4,
				Priority:  1024,
				Gw:        net.ParseIP("192.0.2.1"),
				LinkIndex: 999, // nonexistent: Interface stays blank
			},
			Route{Family: "ipv4", Gateway: "192.0.2.1", Metric: 1024, Attr: map[string]string{}},
		},
		{
			"tuned initcwnd/initrwnd",
			netlink.Route{
				Family:    netlink.FAMILY_V4,
				Priority:  1024,
				Gw:        net.ParseIP("192.0.2.1"),
				LinkIndex: 999,
				InitCwnd:  30,
				InitRwnd:  30,
			},
			Route{
				Family:  "ipv4",
				Gateway: "192.0.2.1",
				Metric:  1024,
				Attr:    map[string]string{"initcwnd": "30", "initrwnd": "30"},
			},
		},
		{
			"proto and src",
			netlink.Route{
				Family:    netlink.FAMILY_V4,
				Priority:  1024,
				Gw:        net.ParseIP("192.0.2.1"),
				LinkIndex: 999,
				Protocol:  netlink.RouteProtocol(unix.RTPROT_DHCP),
				Src:       net.ParseIP("192.0.2.10"),
			},
			Route{
				Family:  "ipv4",
				Gateway: "192.0.2.1",
				Metric:  1024,
				Attr:    map[string]string{"proto": "dhcp", "src": "192.0.2.10"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRoute(tc.r); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeRoute(%+v) = %+v; want %+v", tc.r, got, tc.want)
			}
		})
	}
}
