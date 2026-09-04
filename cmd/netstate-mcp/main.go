// Command netstate-mcp is a read-only MCP server that exposes local network
// state over the Model Context Protocol via stdio.
//
// Stdout (fd 1) is reserved exclusively for MCP/JSON-RPC traffic. All logs and
// diagnostics go to stderr (fd 2).
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ckolos/netstate-mcp/internal/network"
)

const (
	serverName    = "netstate-mcp"
	serverVersion = "0.0.1"
)

// outputSchemaForSlice returns an output schema for an object with a single
// required property that is always a non-nil slice.
//
// The SDK infers a slice field as {"type": ["null","array"]}, which is legal
// JSON Schema but is rejected or mishandled by several MCP clients (they read
// "type" as a single string and either reject the tool or drop the constraint).
// These tools always return a non-nil slice, so the accurate, portable schema
// is a plain "array" whose items schema is derived from the element type elem.
func outputSchemaForSlice(prop, desc string, elem reflect.Type) any {
	items, err := jsonschema.ForType(elem, &jsonschema.ForOptions{})
	if err != nil {
		// Element types here are plain structs, so this cannot fail.
		panic(err)
	}
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			prop: {
				Type:        "array",
				Description: desc,
				Items:       items,
			},
		},
		Required:             []string{prop},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

// listInterfacesInput is the input for the list_interfaces tool. It takes no
// arguments, so it is an empty struct.
type listInterfacesInput struct{}

// listInterfacesOutput is the structured result of the list_interfaces tool.
type listInterfacesOutput struct {
	Interfaces []network.Interface `json:"interfaces"`
}

// getInterfaceInput is the input for the get_interface tool. The jsonschema tag
// supplies a human-readable description for the "name" property in the tool's
// generated input schema.
type getInterfaceInput struct {
	Name string `json:"name" jsonschema:"the interface name, for example lo or eth0"`
}

// getInterfaceOutput is the structured result of the get_interface tool.
type getInterfaceOutput struct {
	Interface network.Interface `json:"interface"`
}

// getInterfaceStatisticsInput is the input for the get_interface_statistics tool.
type getInterfaceStatisticsInput struct {
	Name string `json:"name" jsonschema:"the interface name, for example lo or eth0"`
}

// getInterfaceStatisticsOutput is the structured result of the
// get_interface_statistics tool.
type getInterfaceStatisticsOutput struct {
	Statistics network.InterfaceStatistics `json:"statistics"`
}

// getDefaultRoutesInput is the input for the get_default_routes tool.
type getDefaultRoutesInput struct {
	Family string `json:"family" jsonschema:"address family: ipv4, ipv6, or all"`
}

// getDefaultRoutesOutput is the structured result of the get_default_routes tool.
type getDefaultRoutesOutput struct {
	Routes []network.Route `json:"routes"`
}

// newServer builds the MCP server and registers every tool against src.
//
// It is separated from main so that tests can construct the server with a fake
// Source and drive it over an in-memory transport — no stdio, no real host, no
// LLM. main just calls newServer with the real platform Source.
func newServer(src network.Source) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, nil)

	// list_interfaces: all local interfaces. Each handler is a thin closure that
	// captures src and calls the matching network package function.
	mcp.AddTool(server, &mcp.Tool{
		Name:         "list_interfaces",
		Description:  "List local network interfaces. Takes no arguments.",
		OutputSchema: outputSchemaForSlice("interfaces", "Local network interfaces.", reflect.TypeOf(network.Interface{})),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listInterfacesInput) (*mcp.CallToolResult, listInterfacesOutput, error) {
		ifaces, err := network.ListInterfaces(ctx, src)
		if err != nil {
			return nil, listInterfacesOutput{}, err
		}
		return nil, listInterfacesOutput{Interfaces: ifaces}, nil
	})

	// get_interface: details for a single interface by name.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_interface",
		Description: "Get details for one local network interface, by name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getInterfaceInput) (*mcp.CallToolResult, getInterfaceOutput, error) {
		iface, err := network.GetInterface(ctx, src, in.Name)
		if err != nil {
			return nil, getInterfaceOutput{}, err
		}
		return nil, getInterfaceOutput{Interface: iface}, nil
	})

	// get_interface_statistics: RX/TX counters for one interface, by name.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_interface_statistics",
		Description: "Get RX/TX byte, packet, error, and drop counters for one interface, by name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getInterfaceStatisticsInput) (*mcp.CallToolResult, getInterfaceStatisticsOutput, error) {
		stats, err := network.GetInterfaceStatistics(ctx, src, in.Name)
		if err != nil {
			return nil, getInterfaceStatisticsOutput{}, err
		}
		return nil, getInterfaceStatisticsOutput{Statistics: stats}, nil
	})

	// get_default_routes: default routes for an address family (ipv4/ipv6/all).
	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_default_routes",
		Description:  "Get default routes for an address family: ipv4, ipv6, or all.",
		OutputSchema: outputSchemaForSlice("routes", "Default routes.", reflect.TypeOf(network.Route{})),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getDefaultRoutesInput) (*mcp.CallToolResult, getDefaultRoutesOutput, error) {
		routes, err := network.GetDefaultRoutes(ctx, src, in.Family)
		if err != nil {
			return nil, getDefaultRoutesOutput{}, err
		}
		return nil, getDefaultRoutesOutput{Routes: routes}, nil
	})

	return server
}

func main() {
	// All diagnostics go to stderr. Never write to stdout.
	logger := log.New(os.Stderr, serverName+": ", log.LstdFlags)

	// ctx is cancelled on SIGINT (Ctrl-C) or SIGTERM for clean shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := newServer(network.NewSource())

	logger.Println("starting stdio MCP server")

	// Both io.EOF (client hung up) and context.Canceled (shutdown signal) are
	// normal ways for the session to end and are not treated as errors.
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil &&
		!errors.Is(err, io.EOF) &&
		!errors.Is(err, context.Canceled) {
		logger.Printf("server error: %v", err)
		os.Exit(1)
	}

	logger.Println("server stopped")
}
