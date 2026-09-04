// This file is compiled only on Linux (selected by the _linux.go filename
// suffix). It is the sole file that imports the netlink library, because that
// library does not compile on macOS.
package network

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/vishvananda/netlink"
)

// linuxSource implements the Source contract using the kernel's netlink
// (rtnetlink) protocol via github.com/vishvananda/netlink. It holds no state.
type linuxSource struct{}

func newLinuxSource() Source { return linuxSource{} }

// newPlatformSource is the Linux implementation of the selector NewSource calls.
// Exactly one newPlatformSource exists per build.
func newPlatformSource() Source { return newLinuxSource() }

// Interfaces lists every link via netlink and normalizes each into the domain
// Interface type.
func (linuxSource) Interfaces(ctx context.Context) ([]Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing links via netlink: %w", err)
	}

	out := make([]Interface, 0, len(links))
	for _, link := range links {
		out = append(out, normalizeLink(link))
	}
	return out, nil
}

// InterfaceStats lists every link via netlink and extracts its RX/TX counters.
func (linuxSource) InterfaceStats(ctx context.Context) ([]InterfaceStatistics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing links via netlink: %w", err)
	}

	out := make([]InterfaceStatistics, 0, len(links))
	for _, link := range links {
		out = append(out, linkStatistics(link))
	}
	return out, nil
}

// DefaultRoutes lists routes for the requested family and keeps only the
// default routes (those with no destination prefix).
func (linuxSource) DefaultRoutes(ctx context.Context, family AddressFamily) ([]Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	routes, err := netlink.RouteList(nil, netlinkFamily(family))
	if err != nil {
		return nil, fmt.Errorf("listing routes via netlink: %w", err)
	}

	out := make([]Route, 0)
	for _, r := range routes {
		// Skip routes that carry a destination prefix: only prefix-less
		// (default) routes are of interest here.
		if !isDefaultRoute(r) {
			continue
		}
		out = append(out, normalizeRoute(r))
	}
	return out, nil
}

// normalizeLink converts a netlink.Link into the domain Interface. It is a pure
// function (no I/O), so it can be unit-tested with a hand-built link.
func normalizeLink(link netlink.Link) Interface {
	a := link.Attrs()
	return Interface{
		Name:      a.Name,
		Index:     a.Index,
		MTU:       a.MTU,
		MAC:       a.HardwareAddr.String(),
		Up:        a.Flags&net.FlagUp != 0,
		OperState: a.OperState.String(),
	}
}

// linkStatistics converts a netlink.Link's counters into InterfaceStatistics.
// Statistics can be nil for some links, in which case the counters stay zero.
func linkStatistics(link netlink.Link) InterfaceStatistics {
	a := link.Attrs()
	s := InterfaceStatistics{Name: a.Name}
	if st := a.Statistics; st != nil {
		s.RxBytes = st.RxBytes
		s.RxPackets = st.RxPackets
		s.RxErrors = st.RxErrors
		s.RxDropped = st.RxDropped
		s.TxBytes = st.TxBytes
		s.TxPackets = st.TxPackets
		s.TxErrors = st.TxErrors
		s.TxDropped = st.TxDropped
	}
	return s
}

// isDefaultRoute reports whether a netlink route is a default route: one with
// no destination prefix. Both a nil Dst and a zero-length prefix (e.g.
// 0.0.0.0/0) mean "matches everything", so either counts as default.
func isDefaultRoute(r netlink.Route) bool {
	if r.Dst == nil {
		return true
	}
	ones, _ := r.Dst.Mask.Size()
	return ones == 0
}

// normalizeRoute converts a netlink.Route (already known to be a default route)
// into the domain Route. It resolves the outgoing interface name from the link
// index; if that lookup fails the interface is simply left blank.
func normalizeRoute(r netlink.Route) Route {
	out := Route{
		Family: routeFamily(r),
		Metric: r.Priority,
	}
	if r.Gw != nil {
		out.Gateway = r.Gw.String()
	}
	if link, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
		out.Interface = link.Attrs().Name
	}
	out.Attr = routeAttrs(r)
	return out
}

// routeAttrs folds the route attributes that are not already exposed as core
// Route fields into a map. The netlink Route's attributes are heterogeneous
// (ints, strings, IPs, enums), so a generic map is the only shape that can
// carry them all without a field per attribute. Keys use iproute2's names
// (initcwnd, initrwnd, src, proto...). Only set/non-zero attributes appear, so
// an unadorned route yields an empty (omitted) map.
func routeAttrs(r netlink.Route) map[string]string {
	attr := make(map[string]string)
	if r.InitCwnd != 0 {
		attr["initcwnd"] = strconv.Itoa(r.InitCwnd)
	}
	if r.InitRwnd != 0 {
		attr["initrwnd"] = strconv.Itoa(r.InitRwnd)
	}
	if r.Src != nil {
		attr["src"] = r.Src.String()
	}
	if r.Protocol != 0 {
		attr["proto"] = r.Protocol.String()
	}
	return attr
}

// netlinkFamily maps a domain AddressFamily to the netlink family constant.
func netlinkFamily(family AddressFamily) int {
	switch family {
	case AddressFamilyIPv4:
		return netlink.FAMILY_V4
	case AddressFamilyIPv6:
		return netlink.FAMILY_V6
	default:
		return netlink.FAMILY_ALL
	}
}

// routeFamily returns "ipv4" or "ipv6" for a route, falling back to inferring
// from the gateway address when the family field is not set.
func routeFamily(r netlink.Route) string {
	switch r.Family {
	case netlink.FAMILY_V4:
		return "ipv4"
	case netlink.FAMILY_V6:
		return "ipv6"
	}
	if r.Gw != nil {
		if r.Gw.To4() != nil {
			return "ipv4"
		}
		return "ipv6"
	}
	return "unknown"
}
