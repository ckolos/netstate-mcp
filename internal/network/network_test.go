package network

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeSource is a test double implementing the Source contract. It returns
// canned domain values (and optionally canned errors) instead of touching the
// host, so these tests run identically on any OS. Tests pass it directly to the
// package functions as the Source argument.
type fakeSource struct {
	ifaces    []Interface
	ifacesErr error
	stats     []InterfaceStatistics
	statsErr  error
	routes    []Route
	routesErr error
}

func (f fakeSource) Interfaces(_ context.Context) ([]Interface, error) {
	return f.ifaces, f.ifacesErr
}

func (f fakeSource) InterfaceStats(_ context.Context) ([]InterfaceStatistics, error) {
	return f.stats, f.statsErr
}

func (f fakeSource) DefaultRoutes(_ context.Context, _ AddressFamily) ([]Route, error) {
	return f.routes, f.routesErr
}

func TestParseAddressFamily(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    AddressFamily
		wantErr bool
	}{
		{name: "ipv4", input: "ipv4", want: AddressFamilyIPv4},
		{name: "ipv6", input: "ipv6", want: AddressFamilyIPv6},
		{name: "all", input: "all", want: AddressFamilyAll},
		{name: "empty", input: "", wantErr: true},
		{name: "unknown", input: "ipv5", wantErr: true},
		{name: "wrong case", input: "IPv4", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAddressFamily(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAddressFamily(%q) = %q, nil; want an error", tc.input, got)
				}
				if !errors.Is(err, ErrInvalidAddressFamily) {
					t.Fatalf("ParseAddressFamily(%q) error = %v; want errors.Is(..., ErrInvalidAddressFamily)", tc.input, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseAddressFamily(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParseAddressFamily(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateInterfaceName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "loopback", input: "lo"},
		{name: "ethernet", input: "eth0"},
		{name: "predictable", input: "enp0s3"},
		{name: "max length", input: strings.Repeat("a", maxInterfaceNameLen)},
		{name: "empty", input: "", wantErr: true},
		{name: "too long", input: strings.Repeat("a", maxInterfaceNameLen+1), wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "dotdot", input: "..", wantErr: true},
		{name: "slash", input: "eth/0", wantErr: true},
		{name: "space", input: "eth 0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInterfaceName(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateInterfaceName(%q) = nil; want an error", tc.input)
				}
				if !errors.Is(err, ErrInvalidInterfaceName) {
					t.Fatalf("validateInterfaceName(%q) error = %v; want errors.Is(..., ErrInvalidInterfaceName)", tc.input, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("validateInterfaceName(%q) unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestListInterfaces(t *testing.T) {
	want := []Interface{
		{Name: "lo", Index: 1, MTU: 65536, Up: true, OperState: "unknown"},
		{Name: "eth0", Index: 2, MTU: 1500, MAC: "de:ad:be:ef:00:01", Up: true, OperState: "up"},
	}

	got, err := ListInterfaces(context.Background(), fakeSource{ifaces: want})
	if err != nil {
		t.Fatalf("ListInterfaces returned error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ListInterfaces returned %d interfaces; want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("interface %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestGetInterface passes a fake Source to GetInterface, so it is fully
// deterministic and OS-independent. The not-found and invalid-name logic being
// exercised lives in GetInterface itself, not in the Source.
func TestGetInterface(t *testing.T) {
	src := fakeSource{ifaces: []Interface{
		{Name: "lo", Index: 1, MTU: 65536, Up: true, OperState: "unknown"},
		{Name: "eth0", Index: 2, MTU: 1500, MAC: "de:ad:be:ef:00:01", Up: true, OperState: "up"},
	}}

	t.Run("found", func(t *testing.T) {
		got, err := GetInterface(context.Background(), src, "eth0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := Interface{Name: "eth0", Index: 2, MTU: 1500, MAC: "de:ad:be:ef:00:01", Up: true, OperState: "up"}
		if got != want {
			t.Fatalf("GetInterface(eth0) = %+v; want %+v", got, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := GetInterface(context.Background(), src, "eth9")
		if !errors.Is(err, ErrInterfaceNotFound) {
			t.Fatalf("GetInterface(eth9) error = %v; want errors.Is(..., ErrInterfaceNotFound)", err)
		}
	})

	t.Run("invalid name", func(t *testing.T) {
		_, err := GetInterface(context.Background(), src, "")
		if !errors.Is(err, ErrInvalidInterfaceName) {
			t.Fatalf("GetInterface(\"\") error = %v; want errors.Is(..., ErrInvalidInterfaceName)", err)
		}
	})

	t.Run("source error", func(t *testing.T) {
		failing := fakeSource{ifacesErr: errors.New("boom")}
		if _, err := GetInterface(context.Background(), failing, "eth0"); err == nil {
			t.Fatalf("GetInterface with failing source: want an error, got nil")
		}
	})
}

// TestGetInterfaceStatistics mirrors TestGetInterface: found, not-found,
// invalid-name, and source-error, all against a fake Source.
func TestGetInterfaceStatistics(t *testing.T) {
	src := fakeSource{stats: []InterfaceStatistics{
		{Name: "lo", RxBytes: 100, RxPackets: 2, TxBytes: 100, TxPackets: 2},
		{Name: "eth0", RxBytes: 4096, RxPackets: 40, TxBytes: 2048, TxPackets: 20, RxErrors: 1, TxDropped: 3},
	}}

	t.Run("found", func(t *testing.T) {
		got, err := GetInterfaceStatistics(context.Background(), src, "eth0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := InterfaceStatistics{Name: "eth0", RxBytes: 4096, RxPackets: 40, TxBytes: 2048, TxPackets: 20, RxErrors: 1, TxDropped: 3}
		if got != want {
			t.Fatalf("GetInterfaceStatistics(eth0) = %+v; want %+v", got, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := GetInterfaceStatistics(context.Background(), src, "eth9")
		if !errors.Is(err, ErrInterfaceNotFound) {
			t.Fatalf("error = %v; want errors.Is(..., ErrInterfaceNotFound)", err)
		}
	})

	t.Run("invalid name", func(t *testing.T) {
		_, err := GetInterfaceStatistics(context.Background(), src, "")
		if !errors.Is(err, ErrInvalidInterfaceName) {
			t.Fatalf("error = %v; want errors.Is(..., ErrInvalidInterfaceName)", err)
		}
	})

	t.Run("source error", func(t *testing.T) {
		failing := fakeSource{statsErr: errors.New("boom")}
		if _, err := GetInterfaceStatistics(context.Background(), failing, "eth0"); err == nil {
			t.Fatalf("want an error, got nil")
		}
	})
}

// TestGetDefaultRoutes checks family validation and delegation to the Source.
func TestGetDefaultRoutes(t *testing.T) {
	routes := []Route{
		{Family: "ipv4", Interface: "eth0", Gateway: "192.168.1.1", Metric: 100},
		{Family: "ipv6", Interface: "eth0", Gateway: "fe80::1", Metric: 1024},
	}
	src := fakeSource{routes: routes}

	t.Run("valid family", func(t *testing.T) {
		got, err := GetDefaultRoutes(context.Background(), src, "all")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(routes) {
			t.Fatalf("got %d routes; want %d", len(got), len(routes))
		}
		for i := range routes {
			if !reflect.DeepEqual(got[i], routes[i]) {
				t.Fatalf("route %d = %+v; want %+v", i, got[i], routes[i])
			}
		}
	})

	t.Run("invalid family", func(t *testing.T) {
		_, err := GetDefaultRoutes(context.Background(), src, "ipv5")
		if !errors.Is(err, ErrInvalidAddressFamily) {
			t.Fatalf("error = %v; want errors.Is(..., ErrInvalidAddressFamily)", err)
		}
	})

	t.Run("source error", func(t *testing.T) {
		failing := fakeSource{routesErr: errors.New("boom")}
		if _, err := GetDefaultRoutes(context.Background(), failing, "all"); err == nil {
			t.Fatalf("want an error, got nil")
		}
	})
}
