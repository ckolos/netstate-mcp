// Package network retrieves and normalizes read-only local network state.
//
// It knows nothing about MCP transport or tool registration, and nothing about
// any specific operating system. It exposes plain functions that operate on a
// Source (the pluggable, OS-specific data backend), modeled after the way Go's
// own standard library packages (net, os) are structured.
//
// Each OS provides a concrete Source in a build-constrained file
// (source_linux.go, source_darwin.go); NewSource returns the right one via
// newPlatformSource().
package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Domain errors. Callers (for example, MCP tool handlers) can detect these with
// errors.Is and translate them into appropriate responses.
var (
	// ErrInvalidInterfaceName indicates a syntactically invalid interface name.
	ErrInvalidInterfaceName = errors.New("invalid interface name")

	// ErrInterfaceNotFound indicates a syntactically valid name that does not
	// match any interface currently present on the host.
	ErrInterfaceNotFound = errors.New("interface not found")

	// ErrInvalidAddressFamily indicates an unrecognized address family.
	ErrInvalidAddressFamily = errors.New("invalid address family")
)

// AddressFamily selects which IP protocol family a route query applies to.
type AddressFamily string

const (
	AddressFamilyIPv4 AddressFamily = "ipv4"
	AddressFamilyIPv6 AddressFamily = "ipv6"
	AddressFamilyAll  AddressFamily = "all"
)

// maxInterfaceNameLen is the maximum usable length of a Linux interface name.
// IFNAMSIZ is 16 bytes including the trailing NUL, leaving 15 usable bytes.
const maxInterfaceNameLen = 15

// Interface is a normalized, JSON-friendly view of a local network interface.
// The same shape is produced on every OS, regardless of the underlying API.
type Interface struct {
	Name      string `json:"name"`
	Index     int    `json:"index"`
	MTU       int    `json:"mtu"`
	MAC       string `json:"mac,omitempty"`
	Up        bool   `json:"up"`         // administrative state (IFF_UP)
	OperState string `json:"oper_state"` // operational state (RFC 2863): up, down, unknown, ...
}

// InterfaceStatistics holds RX/TX counters for one interface. Counters are
// cumulative since boot (or since the interface was created).
type InterfaceStatistics struct {
	Name      string `json:"name"`
	RxBytes   uint64 `json:"rx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxErrors  uint64 `json:"rx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxBytes   uint64 `json:"tx_bytes"`
	TxPackets uint64 `json:"tx_packets"`
	TxErrors  uint64 `json:"tx_errors"`
	TxDropped uint64 `json:"tx_dropped"`
}

// Route is a normalized view of a single default route.
//
// Attr holds optional extra route attributes as strings. The Linux kernel and
// BSD route tables attach a variable set of attributes to routes (TCP window
// tuning like initcwnd/initrwnd, source address, protocol origin, onlink flag,
// ...); rather than enumerate every one as a typed field, they are folded into
// this map keyed by the attribute's usual name. macOS never sets any, so it is
// omitempty. Two default routes that look identical in the core fields can
// still be distinct because of these attributes.
type Route struct {
	Family    string            `json:"family"`              // "ipv4" or "ipv6"
	Interface string            `json:"interface,omitempty"` // outgoing interface name, if known
	Gateway   string            `json:"gateway,omitempty"`   // next-hop IP, if the route has one
	Metric    int               `json:"metric"`              // route priority/metric
	Attr      map[string]string `json:"attrs,omitempty"`     // extra attributes, keyed by name
}

// Source is the OS-specific data backend: anything that can read raw network
// state for the local host. It returns domain types only, so callers never
// touch netlink, getifaddrs, or any OS specifics.
//
// It is an interface for two concrete reasons: (1) tests pass a fake Source as
// an argument, so most tests do not depend on the live host or the current OS;
// (2) the Linux and macOS backends are interchangeable behind this one contract.
type Source interface {
	Interfaces(ctx context.Context) ([]Interface, error)
	InterfaceStats(ctx context.Context) ([]InterfaceStatistics, error)
	DefaultRoutes(ctx context.Context, family AddressFamily) ([]Route, error)
}

// NewSource returns the Source implementation for the current operating system.
//
// It returns the interface rather than a concrete type because the concrete
// type differs per OS (netlink-backed on Linux, BSD-route-backed on macOS);
// the actual selection is made at compile time by newPlatformSource, which is
// defined in exactly one build-constrained file.
func NewSource() Source {
	return newPlatformSource()
}

// ListInterfaces returns all local network interfaces from src, normalized. It
// respects ctx cancellation and never requires root.
func ListInterfaces(ctx context.Context, src Source) ([]Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return src.Interfaces(ctx)
}

// GetInterface returns the single interface named name from src. It returns
// ErrInvalidInterfaceName for a malformed name and ErrInterfaceNotFound when no
// interface with that name exists on the host.
//
// The validation and not-found logic lives here, in shared code, so it is not
// duplicated in each OS backend.
func GetInterface(ctx context.Context, src Source, name string) (Interface, error) {
	if err := ctx.Err(); err != nil {
		return Interface{}, err
	}
	if err := validateInterfaceName(name); err != nil {
		return Interface{}, err
	}

	ifaces, err := src.Interfaces(ctx)
	if err != nil {
		return Interface{}, fmt.Errorf("looking up interface %q: %w", name, err)
	}

	for _, iface := range ifaces {
		if iface.Name == name {
			return iface, nil
		}
	}
	return Interface{}, fmt.Errorf("%w: %q", ErrInterfaceNotFound, name)
}

// GetInterfaceStatistics returns RX/TX counters for the interface named name.
// Like GetInterface, it validates the name and returns ErrInterfaceNotFound if
// no such interface exists. The validation and not-found logic lives here so it
// is not duplicated per OS.
func GetInterfaceStatistics(ctx context.Context, src Source, name string) (InterfaceStatistics, error) {
	if err := ctx.Err(); err != nil {
		return InterfaceStatistics{}, err
	}
	if err := validateInterfaceName(name); err != nil {
		return InterfaceStatistics{}, err
	}

	stats, err := src.InterfaceStats(ctx)
	if err != nil {
		return InterfaceStatistics{}, fmt.Errorf("looking up statistics for %q: %w", name, err)
	}

	for _, s := range stats {
		if s.Name == name {
			return s, nil
		}
	}
	return InterfaceStatistics{}, fmt.Errorf("%w: %q", ErrInterfaceNotFound, name)
}

// GetDefaultRoutes returns the host's default routes for the given address
// family. family must be "ipv4", "ipv6", or "all"; anything else yields
// ErrInvalidAddressFamily. The result is always non-nil (possibly empty).
func GetDefaultRoutes(ctx context.Context, src Source, family string) ([]Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	af, err := ParseAddressFamily(family)
	if err != nil {
		return nil, err
	}

	routes, err := src.DefaultRoutes(ctx, af)
	if err != nil {
		return nil, fmt.Errorf("listing default routes: %w", err)
	}
	return routes, nil
}

// ParseAddressFamily validates and normalizes a route address-family string.
// Accepted values are exactly "ipv4", "ipv6", and "all" (lowercase). Any other
// value, including the empty string, yields ErrInvalidAddressFamily.
func ParseAddressFamily(s string) (AddressFamily, error) {
	switch AddressFamily(s) {
	case AddressFamilyIPv4, AddressFamilyIPv6, AddressFamilyAll:
		return AddressFamily(s), nil
	default:
		return "", fmt.Errorf("%w: %q (want ipv4, ipv6, or all)", ErrInvalidAddressFamily, s)
	}
}

// validateInterfaceName reports whether name is a syntactically plausible
// interface name. It does NOT check whether the interface exists on the host.
//
// The rules mirror the Linux kernel's conservative checks: non-empty, at most
// maxInterfaceNameLen bytes, not "." or "..", and no whitespace or '/'.
func validateInterfaceName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: name is empty", ErrInvalidInterfaceName)
	case len(name) > maxInterfaceNameLen:
		return fmt.Errorf("%w: %q exceeds %d bytes", ErrInvalidInterfaceName, name, maxInterfaceNameLen)
	case name == "." || name == "..":
		return fmt.Errorf("%w: %q is not a permitted name", ErrInvalidInterfaceName, name)
	case strings.ContainsAny(name, " \t\n\r/"):
		return fmt.Errorf("%w: %q contains whitespace or '/'", ErrInvalidInterfaceName, name)
	}
	return nil
}
