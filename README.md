# netstate-mcp

A read-only [Model Context Protocol](https://modelcontextprotocol.io) (MCP)
server that exposes structured, local network state over stdio.

It never modifies system state: no running commands, no changing
routes/addresses, no controlling interfaces. It reads network information
directly from the kernel and returns stable, JSON-shaped data.

## Platforms

| OS      | Status                                                                                                                                             |
| ------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Linux   | Fully implemented and tested (via netlink)                                                                                                         |
| macOS   | Implemented and validated on real hardware (stdlib `net` + BSD routing socket via `golang.org/x/net/route`)                                        |
| Windows | Not supported                                                                                                                                      |

The tools are named the same and return the same shape on every OS; only the
underlying system calls differ. The OS backend is selected at compile time by
build constraints (`source_linux.go`, `source_darwin.go`), with no runtime OS
branching in the logic.

## Requirements

- Go 1.27 or newer.
- Linux or macOS. Both paths are implemented and validated on their own
  hardware (Linux via netlink; macOS via stdlib `net` plus the BSD routing
  socket, tested on macOS 15.7.9).
- No root required — netlink and routing-socket reads are unprivileged.

## Build

```sh
go build -o ./bin/netstate-mcp ./cmd/netstate-mcp
```

(The binary goes in `bin/` on purpose — never build a bare `./netstate-mcp` at
the repo root, which would collide with the project directory name.)

## Tools

| Tool                       | Input                                | Returns                                                                     |
| -------------------------- | ------------------------------------ | --------------------------------------------------------------------------- |
| `list_interfaces`          | none                                 | All local interfaces (name, index, MTU, MAC, admin `up`, operational state) |
| `get_interface`            | `name`                               | Details for one interface                                                   |
| `get_interface_statistics` | `name`                               | RX/TX bytes, packets, errors, drops for one interface                       |
| `get_default_routes` | `family` = `ipv4` \| `ipv6` \| `all` | Default routes (interface, gateway, metric, family, plus a generic `attrs` map with any extra route attributes, e.g. TCP initcwnd/initrwnd on Linux) |

Invalid interface names return an `interface not found` / `invalid interface
name` error; an unknown address family returns `invalid address family`.

## Run

The server speaks MCP/JSON-RPC over **stdin/stdout**. Stdout is reserved
exclusively for protocol traffic; all logs go to stderr. It is meant to be
launched by an MCP client, not run interactively.

### With the MCP Inspector

The official [MCP Inspector](https://github.com/modelcontextprotocol/inspector)
is a protocol client / test harness (not an LLM). Point it at the built binary:

```sh
go build -o ./bin/netstate-mcp ./cmd/netstate-mcp
npx @modelcontextprotocol/inspector ./bin/netstate-mcp
```

It opens a local web UI where you can run `initialize`, list tools, and invoke
each tool with arguments — no LLM required.

### Manual stdio smoke test

Build first, then pipe JSON-RPC messages in. The `sleep` holds stdin open long
enough for buffered requests to be processed before EOF:

```sh
go build -o ./bin/netstate-mcp ./cmd/netstate-mcp
{ printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}' \
'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
'{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
'{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_interfaces","arguments":{}}}' ; sleep 0.5 ; } | ./bin/netstate-mcp
```

A clean shutdown (stdin EOF or SIGINT/SIGTERM) exits 0.

## Testing

```sh
go test ./...           # all tests
go test -race ./...     # with the race detector
go vet ./...
gofmt -l .              # prints nothing when formatted
```

Cross-compile check for macOS (from any OS):

```sh
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
```

Tests are layered: OS-independent unit tests use a fake data source; a
Linux-only integration test checks the real loopback interface; and an
in-process MCP protocol test (`cmd/netstate-mcp/main_test.go`) exercises
`initialize`, `tools/list`, and valid/invalid `tools/call` with no LLM.

## Layout

```
cmd/netstate-mcp/main.go     # entry point + MCP tool registration
internal/network/
  network.go                 # domain types, validation, Source contract, functions
  source_linux.go            # netlink backend        (Linux build only)
  source_darwin.go           # macOS backend          (macOS build only)
  *_test.go                  # shared + per-OS tests
```

## Design notes

- **Read-only.** No write-capable tools, no command execution, no arbitrary
  paths or subcommands.
- **Stdout is sacred.** Only JSON-RPC on fd 1; diagnostics on fd 2.
- **Package of functions + a pluggable `Source`**, modeled on Go's standard
  library (`net`, `os`). `NewSource()` returns the OS backend; tests pass a fake
  `Source` as an argument.
- **Structured output.** Tools return typed values, so the SDK emits both a
  human-readable text block and a schema-validated `structuredContent` object.

## License

TBD.
