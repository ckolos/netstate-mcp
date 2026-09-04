package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ckolos/netstate-mcp/internal/network"
)

// stubSource implements network.Source with canned, deterministic data so the
// protocol test does not depend on the host or the current OS.
type stubSource struct{}

func (stubSource) Interfaces(context.Context) ([]network.Interface, error) {
	return []network.Interface{
		{Name: "lo", Index: 1, MTU: 65536, Up: true, OperState: "unknown"},
	}, nil
}

func (stubSource) InterfaceStats(context.Context) ([]network.InterfaceStatistics, error) {
	return []network.InterfaceStatistics{{Name: "lo"}}, nil
}

func (stubSource) DefaultRoutes(context.Context, network.AddressFamily) ([]network.Route, error) {
	return []network.Route{}, nil
}

// TestServerProtocol drives the real server over an in-memory transport using
// the SDK's own client: initialize (via Connect), tools/list, a valid
// tools/call, and two invalid tools/call variants. No stdio, no LLM.
func TestServerProtocol(t *testing.T) {
	ctx := context.Background()

	server := newServer(stubSource{})
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	// client.Connect performs the initialize / initialized handshake.
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect (initialize): %v", err)
	}
	defer cs.Close()

	// tools/list — every registered tool must be present.
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool)
	for _, tool := range lt.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"list_interfaces", "get_interface", "get_interface_statistics", "get_default_routes"} {
		if !got[want] {
			t.Fatalf("tools/list missing %q; got %v", want, got)
		}
	}

	// Valid tools/call — list_interfaces succeeds and mentions the stub's "lo".
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_interfaces", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(list_interfaces): %v", err)
	}
	if res.IsError {
		t.Fatalf("list_interfaces returned an error result: %+v", res.Content)
	}
	if text := toolText(res); !strings.Contains(text, "lo") {
		t.Fatalf("list_interfaces result does not mention lo: %q", text)
	}

	// Invalid tools/call (tool-level error): a valid-but-absent interface name
	// yields a result with IsError set, NOT a protocol error.
	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_interface", Arguments: map[string]any{"name": "nope0"}})
	if err != nil {
		t.Fatalf("CallTool(get_interface) unexpected transport error: %v", err)
	}
	if !res2.IsError {
		t.Fatalf("get_interface with an absent name should be an error result; got %+v", res2)
	}

	// Invalid tools/call (unknown tool): this is a protocol-level error, so
	// CallTool returns a non-nil error.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "does_not_exist", Arguments: map[string]any{}}); err == nil {
		t.Fatalf("CallTool(does_not_exist) should return an error")
	}
}

// toolText concatenates the text of a result's TextContent blocks.
func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestSliceOutputSchemasAreNotNullable guards against the SDK serializing a
// slice output field as {"type": ["null","array"]}. That array form is legal
// JSON Schema but several MCP clients read "type" as a single string and
// either reject the tool or drop the constraint (seen as a portability warning
// in the MCP Inspector). The two list-shaped tools always return a non-nil
// slice, so their "routes"/"interfaces" property must be a plain "array".
func TestSliceOutputSchemasAreNotNullable(t *testing.T) {
	server := newServer(stubSource{})
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer cs.Close()

	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	for _, tc := range []struct {
		name string
		prop string
	}{
		{"get_default_routes", "routes"},
		{"list_interfaces", "interfaces"},
	} {
		var tool *mcp.Tool
		for _, t := range lt.Tools {
			if t.Name == tc.name {
				tool = t
				break
			}
		}
		if tool == nil {
			t.Fatalf("tool %q not listed", tc.name)
		}
		// The wire schema is a JSON object; fetch it as raw JSON to assert the
		// exact "type" keyword without depending on SDK schema internals.
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal %s outputSchema: %v", tc.name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal %s outputSchema %s: %v", tc.name, raw, err)
		}
		prop, ok := schema.Properties[tc.prop]
		if !ok {
			t.Fatalf("%s outputSchema missing property %q: %s", tc.name, tc.prop, raw)
		}
		if prop.Type == "" {
			t.Fatalf("%s.%s has no single-string type (nullable union?): %s", tc.name, tc.prop, raw)
		}
		if prop.Type != "array" {
			t.Fatalf("%s.%s type = %q; want \"array\"", tc.name, tc.prop, prop.Type)
		}
	}
}
