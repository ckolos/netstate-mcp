// This file is compiled only on macOS (selected by the _darwin.go filename
// suffix). It implements the Source contract using BSD facilities:
//
//   - stdlib net for the interface list,
//   - the routing socket (via golang.org/x/net/route) for the route table and
//     the NET_RT_IFLIST2 interface list that carries per-interface counters.
//
// It never shells out. This code is written against the documented Darwin
// structures but is exercised on macOS only (it does not compile or run on
// Linux), so its integration tests live in source_darwin_test.go.
package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// darwinSource implements the Source contract on macOS. It holds no state.
type darwinSource struct{}

func newDarwinSource() Source { return darwinSource{} }

// newPlatformSource is the macOS implementation of the selector NewSource calls.
// Exactly one newPlatformSource exists per build.
func newPlatformSource() Source { return newDarwinSource() }

// Interfaces lists local interfaces via the standard library. The stdlib does
// not expose operational (carrier) state on macOS, so OperState is reported as
// "unknown"; Up reflects the administrative flag.
func (darwinSource) Interfaces(ctx context.Context) ([]Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}

	out := make([]Interface, 0, len(ifaces))
	for _, iface := range ifaces {
		out = append(out, Interface{
			Name:      iface.Name,
			Index:     iface.Index,
			MTU:       iface.MTU,
			MAC:       iface.HardwareAddr.String(),
			Up:        iface.Flags&net.FlagUp != 0,
			OperState: "unknown",
		})
	}
	return out, nil
}

// InterfaceStats reads RX/TX counters from the NET_RT_IFLIST2 routing dump,
// which carries an if_data64 (with counters) per interface.
func (darwinSource) InterfaceStats(ctx context.Context) ([]InterfaceStatistics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	names, err := interfaceNamesByIndex()
	if err != nil {
		return nil, err
	}

	b, err := route.FetchRIB(unix.AF_UNSPEC, unix.NET_RT_IFLIST2, 0)
	if err != nil {
		return nil, fmt.Errorf("fetching interface list via route sysctl: %w", err)
	}

	var out []InterfaceStatistics
	for len(b) >= 4 {
		msglen := int(binary.LittleEndian.Uint16(b[:2]))
		if msglen < 4 || msglen > len(b) {
			break
		}
		if b[3] == unix.RTM_IFINFO2 {
			if s, ok := parseIfMsghdr2(b[:msglen], names); ok {
				out = append(out, s)
			}
		}
		b = b[msglen:]
	}
	return out, nil
}

// DefaultRoutes reads the route table for the requested family and keeps only
// gateway routes with a zero destination (the default route).
func (darwinSource) DefaultRoutes(ctx context.Context, family AddressFamily) ([]Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b, err := route.FetchRIB(darwinRouteFamily(family), unix.NET_RT_DUMP, 0)
	if err != nil {
		return nil, fmt.Errorf("fetching route table via route sysctl: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, b)
	if err != nil {
		return nil, fmt.Errorf("parsing route table: %w", err)
	}

	out := make([]Route, 0)
	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok {
			continue
		}
		if rm.Flags&unix.RTF_GATEWAY == 0 {
			continue
		}
		if r, ok := defaultRoute(rm); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// interfaceNamesByIndex builds an index->name map from the stdlib interface
// list, so the counter records (which carry only an index) can be named.
func interfaceNamesByIndex() (map[int]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	m := make(map[int]string, len(ifaces))
	for _, iface := range ifaces {
		m[iface.Index] = iface.Name
	}
	return m, nil
}

// parseIfMsghdr2 extracts counters from one if_msghdr2 message. Field offsets
// come from the unix.IfMsghdr2 / unix.IfData64 struct definitions (which match
// the C layout), and values are read with encoding/binary so the code does not
// depend on the message buffer being aligned for an unsafe struct cast.
//
// Subtlety: unsafe.Offsetof(m.Data.Ibytes) is relative to the innermost struct
// (m.Data), NOT to the outer message m — Go defines Offsetof of a nested field
// as its offset within that field's own struct type. The counter offsets must
// therefore add the base offset of Data inside IfMsghdr2, or every read lands
// 32 bytes early (inside the header). Offsetof(m.Index) needs no adjustment:
// Index is a direct member of m.
func parseIfMsghdr2(msg []byte, names map[int]string) (InterfaceStatistics, bool) {
	var m unix.IfMsghdr2
	if uintptr(len(msg)) < unsafe.Sizeof(m) {
		return InterfaceStatistics{}, false
	}

	u16 := func(off uintptr) uint16 { return binary.LittleEndian.Uint16(msg[off:]) }
	u64 := func(off uintptr) uint64 { return binary.LittleEndian.Uint64(msg[off:]) }

	// Base offset of the embedded if_data64 inside the message, so counter
	// offsets (which are relative to Data) can be made message-relative.
	dataBase := unsafe.Offsetof(m.Data)

	index := int(u16(unsafe.Offsetof(m.Index)))
	return InterfaceStatistics{
		Name:      names[index],
		RxBytes:   u64(dataBase + unsafe.Offsetof(m.Data.Ibytes)),
		RxPackets: u64(dataBase + unsafe.Offsetof(m.Data.Ipackets)),
		RxErrors:  u64(dataBase + unsafe.Offsetof(m.Data.Ierrors)),
		RxDropped: u64(dataBase + unsafe.Offsetof(m.Data.Iqdrops)),
		TxBytes:   u64(dataBase + unsafe.Offsetof(m.Data.Obytes)),
		TxPackets: u64(dataBase + unsafe.Offsetof(m.Data.Opackets)),
		TxErrors:  u64(dataBase + unsafe.Offsetof(m.Data.Oerrors)),
		// macOS if_data64 has no output-drop counter, so TxDropped stays 0.
		TxDropped: 0,
	}, true
}

// defaultRoute converts a gateway route message into a domain Route if it is a
// default route (zero destination). ok is false otherwise.
func defaultRoute(rm *route.RouteMessage) (Route, bool) {
	if len(rm.Addrs) <= unix.RTAX_DST {
		return Route{}, false
	}

	var family string
	switch dst := rm.Addrs[unix.RTAX_DST].(type) {
	case *route.Inet4Addr:
		if dst.IP != ([4]byte{}) {
			return Route{}, false
		}
		family = "ipv4"
	case *route.Inet6Addr:
		if dst.IP != ([16]byte{}) {
			return Route{}, false
		}
		family = "ipv6"
	default:
		return Route{}, false
	}

	out := Route{Family: family}

	if len(rm.Addrs) > unix.RTAX_GATEWAY {
		switch gw := rm.Addrs[unix.RTAX_GATEWAY].(type) {
		case *route.Inet4Addr:
			out.Gateway = net.IP(gw.IP[:]).String()
		case *route.Inet6Addr:
			out.Gateway = net.IP(gw.IP[:]).String()
		}
	}

	if iface, err := net.InterfaceByIndex(rm.Index); err == nil {
		out.Interface = iface.Name
	}

	return out, true
}

// darwinRouteFamily maps a domain AddressFamily to the BSD address family used
// when fetching the route table.
func darwinRouteFamily(family AddressFamily) int {
	switch family {
	case AddressFamilyIPv4:
		return unix.AF_INET
	case AddressFamilyIPv6:
		return unix.AF_INET6
	default:
		return unix.AF_UNSPEC
	}
}
