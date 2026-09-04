# AGENTS.md — working agreements & project brief for `netstate-mcp`

This file is the source of truth for how work is done in this repository and why.
It captures both the **project definition** and the **collaboration style** agreed
with the maintainer. Read it fully before making changes.

---

## 0. Who the maintainer is (and what that means for you)

- The maintainer is an experienced **Linux / infrastructure engineer**, but a
  **beginner in Go** and not a career software developer. The maintainer knows
  **Python** better than Go; analogies to Python are welcome and effective.
- Therefore: **explain the _why_, not just the _what_.** Any structural or design
  change (a new interface, a new type, a file split, a dependency) must come with
  the concrete reason it exists, the problem it solves, and what the alternative
  would have cost. "Trust me" and unexplained abstractions are not acceptable.
- If a change is justified by a _future_ need, **name that future need
  explicitly** (e.g. "the macOS source in a later step is a second implementation
  of this same contract"). Do not hand-wave "we might need it."
- **Define jargon on first use.** Do not drop acronyms or terms of art as if they
  are shared vocabulary (e.g. YAGNI = "You Aren't Gonna Need It", DI = "dependency
  injection", build constraint = "a rule for which OS a file compiles on").
- Learning is understanding _why_. Anyone can make and describe a change; the
  value here is in understanding the reasoning.

---

## 1. Collaboration workflow

Work in **small, sequential steps**. Do not generate the whole repository or the
whole feature at once.

**Step gating:** After finishing a step, **stop and wait** for the maintainer to
say they are ready before starting, presenting, or previewing the next step. Do
not begin the next step's code or tease its concepts unprepared. Acceptable "go"
signals are explicit, e.g. "next", "resume Step N", "I'm ready".

**Presentation order for each step (code-first):**

1. One- or two-sentence statement of the immediate goal.
2. A one-line heads-up of the genuinely new concepts to watch for.
3. The exact shell commands to run.
4. The **complete contents** of only the file(s) created or changed.
5. A **concept walkthrough anchored to the code just shown** — quote the actual
   snippet, **name the file it lives in**, then explain the Go concept. Concepts
   are explained _after_ the code, never in the abstract beforehand.
6. Where each file goes and why.
7. Exact verification commands.
8. What successful output/behavior looks like.
9. Likely errors and how to diagnose them.
10. Stop and wait.

**When editing files directly:** after applying changes, summarize what was done
with **file names and line numbers**, and include enough surrounding context in
the summary that the maintainer does not need to re-open the file to follow it
(but can if they wish).

**Corrections are welcome, not interruptions.** If the maintainer challenges a
decision, treat it as a legitimate design question and answer honestly —
including reconsidering or reverting the change.

**NEVER DELETE ANYTHING.** Do not use delete_path, `rm`, or any destructive
operation on files or directories, for any reason. Only create and edit files.
If something needs removing, tell the maintainer and let them do it by hand.
(This rule exists because the agent once deleted the entire project directory.)

---

## 2. Project goal

Build a **read-only** MCP (Model Context Protocol) server named `netstate-mcp`
that exposes structured local network state.

**Target platforms: Linux and macOS. No Windows.** Both are implemented.
Linux (netlink) and macOS (stdlib `net` + BSD routing socket) are each
validated on their own hardware (macOS: see Step 12 in §9).

**Hard invariant — read-only:** the server must never modify system state. No
running arbitrary commands, no changing routes/addresses, no controlling
interfaces, no creating namespaces, no privileged network-management actions.

### Initial tool set (identical behavior on every OS)

1. `list_interfaces` — no args; normalized list of local interfaces.
2. `get_interface` — input: interface name; details for one interface (name,
   index, MTU, MAC, admin flags/state, operational state where available).
3. `get_interface_statistics` — input: interface name; RX/TX bytes, packets,
   errors, dropped counters.
4. `get_default_routes` — input: address family `ipv4` | `ipv6` | `all`; default
   route details (interface, gateway if present, metric, address family).

The tools are **named the same, do the same thing, and return the same shape**
regardless of OS. Only the underlying OS calls differ (see §5a).

### Explicitly out of scope for the first version

IP addresses, neighbors/ARP/NDP, bridges/VLANs/bonds, WireGuard metadata, network
namespaces, Docker/veth relationships, snapshots, route comparison, MCP resources,
MCP prompts, HTTP transport, OAuth, databases, Docker packaging, Kubernetes, cloud
APIs, Windows support, and any write-capable tool. Do not add these unless
explicitly requested.

---

## 3. Technology choices (pinned decisions)

- **Language:** Go.
- **Module path:** `github.com/ckolos/netstate-mcp`.
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` (currently `v1.7.0`).
- **Transport:** stdio only for the initial version.
- **OS targets:** Linux and macOS. No Windows.
- **Linux network layer:** `github.com/vishvananda/netlink` (talks the kernel's
  netlink/rtnetlink protocol). Imported **only** from Linux-tagged files, because
  it does not compile on macOS.
- **macOS network layer:** stdlib `net` for the interface list, and the BSD
  routing socket via `golang.org/x/net/route` (with `golang.org/x/sys/unix`
  structs/constants) for the route table and the `NET_RT_IFLIST2` interface
  counters. No cgo, no shelling out.
- **Never shell out** to `ip`, `ifconfig`, `netstat`, `route`, etc. Talk to the
  kernel via libraries/syscalls and return structured data.
- **Testing:** standard Go `testing` package.
- **Extra dependency (macOS only):** `golang.org/x/net` (for `x/net/route`).
  `golang.org/x/sys` is a direct dependency (the darwin file uses `x/sys/unix`).
- **Manual MCP testing:** the official **MCP Inspector**, which is a protocol
  client / test harness — **not** an LLM. The server is fully testable with no LLM
  in the loop.
- **Dev environment:** Unix command line (Linux or macOS). **No PowerShell
  instructions, ever.**

---

## 4. Design constraints & invariants

- **Stdout (fd 1) is reserved exclusively for MCP/JSON-RPC.** All logs and
  diagnostics go to **stderr (fd 2)** only. Never `fmt.Println`, `log.Println`, or
  any application logging on stdout. Use an explicit stderr logger.
- **Never** expose a generic command-execution tool, or accept arbitrary command
  strings, shell fragments, arbitrary CLI subcommands, or arbitrary filesystem
  paths.
- **Validate all tool inputs.**
- Return **stable, structured, JSON-compatible** data — not raw netlink dumps or
  raw CLI output.
- Use `context.Context`; respect cancellation/deadlines where practical.
- **Bound** potentially large results.
- Return useful **domain errors** for missing interfaces and invalid address
  families.
- **Do not require root** for the basic read-only tools if at all avoidable
  (netlink and getifaddrs reads are unprivileged).
- Pin/record dependency versions in `go.mod` / `go.sum`.
- Keep the first version simple. No new infrastructure without an explicit request.

---

## 5. Project layout

Keep this small layout; do not add directories without a clear, stated reason.

```
netstate-mcp/
├── go.mod
├── go.sum
├── AGENTS.md
├── README.md
├── cmd/
│   └── netstate-mcp/
│       └── main.go
└── internal/
    └── network/
        ├── network.go            # shared, OS-agnostic
        ├── source_linux.go       # netlink source        (compiled only on Linux)
        ├── source_darwin.go      # macOS source          (compiled only on macOS)
        ├── network_test.go       # shared tests (fake source, OS-independent)
        ├── source_linux_test.go  # Linux-only real-host checks (e.g. "lo")
        └── source_darwin_test.go # macOS-only real-host checks (e.g. "lo0")
```

### Responsibilities

- **`cmd/netstate-mcp/main.go`** (`package main`): process entry point. Constructs
  dependencies, creates the MCP server, registers tools, starts the stdio
  transport. **Minimal business logic.** Tool handlers here stay thin — decode
  validated input, call the network service, shape the result. Handlers are
  identical for all OSes.
- **`internal/network/network.go`** (`package network`): domain types, input
  validation, normalization helpers, and the **`source` contract** (the interface
  every OS backend implements). Knows nothing about MCP transport, and nothing
  about any specific OS. `NewSource` calls `newPlatformSource()` (see §5a); the
  package exposes plain functions, not a `Service` type.
- **`internal/network/source_linux.go`**: `linuxSource`, implementing the `source`
  contract via `vishvananda/netlink`. The only file that imports netlink.
- **`internal/network/source_darwin.go`**: `darwinSource`, implementing the same
  contract via stdlib `net` and the BSD routing socket (`x/net/route`,
  `x/sys/unix`). Validated on real macOS hardware (macOS 15.7.9, Intel).
- **Tests**: `network_test.go` uses a fake source and runs on any OS.
  `source_linux_test.go` / `source_darwin_test.go` hold OS-specific real-host
  checks (loopback is `lo` on Linux, `lo0` on macOS).

### 5a. Two native paths, one contract, no runtime OS branching

**Requirement:** two clean, fully separate implementations — Linux and macOS —
with **no scattered `if runtime.GOOS == ...` checks** in the logic. The tools look
and behave identically; only the OS calls differ.

**Mechanism — build constraints (filename suffixes):** Go compiles a file named
`*_linux.go` only on Linux and `*_darwin.go` only on macOS, automatically, based on
the filename. The OS decision therefore happens **once, at compile time**.

**Selection without if/else:** each platform file defines the _same_ constructor
signature, and shared code just calls it:

```go
// network.go (compiled everywhere)
func NewSource() Source { return newPlatformSource() }

// source_linux.go  -> func newPlatformSource() Source { return newLinuxSource() }
// source_darwin.go -> func newPlatformSource() Source { return newDarwinSource() }
```

In any given build exactly one `newPlatformSource` exists, so it links directly.
No conditional ever runs.

**macOS status:** the macOS path is implemented natively (no stub). Earlier steps
used a `darwinSource` stub that returned a shared `errNotImplemented` sentinel so
the server still built and listed tools on macOS; that sentinel has been removed
now that every method is real. The macOS code has been run and validated on real
hardware (macOS 15.7.9, Intel; see Step 12 in §9), including counter offsets
cross-checked against `netstat` and default-route detection.

### Architecture (separation of concerns)

```
MCP client / MCP Inspector
        │  stdio (JSON-RPC)
        ▼
cmd/netstate-mcp/main.go   — startup, dependency wiring, starts transport
        │
        ▼
MCP tool handlers          — decode validated input, call network functions, return result
        │
        ▼
internal/network (network.go) — domain types, validation, `source` contract
        │
        ├── source_linux.go   — netlink implementation   (Linux build only)
        └── source_darwin.go  — macOS implementation     (macOS build only)
```

---

## 6. Go conventions adopted in this repo

- **Formatting:** all code is `gofmt`-clean. Indent with tabs (gofmt's default).
- **Errors:** return errors, never panic for expected conditions. Wrap with
  `%w` when a caller may need to inspect the cause via `errors.Is` (e.g. domain
  sentinels like `ErrInterfaceNotFound`). Use `%v` only when the value need not be
  inspectable. (`%w` keeps the error in the chain; `%v` only puts text in the
  message — this distinction matters and has already bitten us once.)
- **Sentinel/domain errors:** package-level `var Err... = errors.New(...)` so
  callers can branch with `errors.Is`.
- **Platform code:** OS-specific implementations live in `*_linux.go` /
  `*_darwin.go` files selected by build constraints — never behind runtime
  `runtime.GOOS` checks in the logic. Shared code calls a single
  `newPlatformSource()` provided by exactly one of those files.
- **Naming:** short names for short scopes, descriptive names for long-lived or
  exported identifiers. For a network interface **value**, use **`iface`** (not
  `ifi`); `interface` and `if` are reserved keywords so the value must be
  abbreviated. Use `i`/`j` only for integer indices.
- **Interfaces:** define a **small** interface at the point of consumption only
  when there is a **concrete reason**. Here the `source` interface has two: (1) so
  tests can inject a fake, and (2) so the Linux and macOS backends are
  interchangeable behind one contract. State the reason when adding one.
- **Dependency injection:** construct the real dependency in `main.go`/`NewSource`
  and inject it; tests inject a fake via an unexported constructor (`fakeSource`).
- **JSON:** exported struct fields with `json:"..."` tags; `omitempty` to drop
  zero-value fields (e.g. a missing MAC). Struct tags are space-separated
  `key:"value"` pairs — a malformed tag (e.g. a missing closing quote) is caught
  by `go vet`'s `structtag` analyzer. A `jsonschema:"..."` tag adds a property
  description to a tool's generated input schema.
- **Typed tool output:** tool handlers return a typed `Out` value (not `any`) so
  the SDK auto-generates both the human-readable `content` block and the
  machine-readable, schema-validated `structuredContent` block, and validates
  input against the inferred schema before the handler runs.

---

## 7. Testing strategy (build in layers)

1. Unit tests for validation and normalization (pure, deterministic, OS-agnostic).
2. **Fakeable** source contract so most tests do not depend on the local machine
   or the current OS.
3. OS-specific real-host integration tests in `source_<os>_test.go` (e.g. loopback
   `lo` on Linux, `lo0` on macOS).
4. Tests confirming invalid and empty interface names return useful, inspectable
   errors (`errors.Is(..., ErrInvalidInterfaceName)`).
5. Later: an MCP protocol-level test (in-process client + in-memory transport) for
   `initialize`, `tools/list`, a valid `tools/call`, and an invalid `tools/call`.
   This avoids the stdin/EOF race of ad-hoc pipe tests and needs no LLM.
6. Manual verification via the MCP Inspector.

Each native path is only fully testable on its own OS; the shared logic is covered
everywhere via the fake source. The application must always be testable **without a
local LLM.**

---

## 8. Standard commands

Verification (run from the repo root):

```
gofmt -l .          # prints nothing when all files are formatted
go vet ./...        # static analysis; silent = clean
go build ./...
go test ./...
go test -race ./...  # race detector; use as a habit
go test -v ./internal/network/
```

Cross-compile sanity check (does the macOS build compile from Linux?):

```
GOOS=darwin GOARCH=arm64 go build ./...   # Apple Silicon
GOOS=darwin GOARCH=amd64 go build ./...   # Intel Macs
```

Manual MCP smoke test over stdio. Build the binary first and pipe into it (piping
into `go run` can race: if compilation takes longer than the stdin-hold, the
server sees EOF immediately). The `{ ...; sleep 0.5; }` wrapper holds stdin open
briefly so buffered requests are processed before EOF. This is a smoke test only;
the deterministic protocol test is the in-process one in §7.

**Never name a build artifact the same as the project directory.** Build into
`bin/` (git-ignored), e.g. `./bin/netstate-mcp` — never a bare `./netstate-mcp`
file at the repo root, which collides with the `netstate-mcp` directory name.

```
go build -o ./bin/netstate-mcp ./cmd/netstate-mcp
{ printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}' \
'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
'{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
'{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_interfaces","arguments":{}}}' ; sleep 0.5 ; } | ./bin/netstate-mcp
```

A clean shutdown (stdin EOF or SIGINT/SIGTERM) is normal and must exit 0; only
genuine transport/protocol failures exit non-zero.

---

## 9. Progress log

- **Step 1 — done.** Runnable skeleton, correct layout, stderr-only logging habit.
- **Step 2 — done.** stdio MCP server; one placeholder tool proved the pipeline
  (later removed).
- **Step 3 — done.** `internal/network` package: domain errors, `AddressFamily`
  type + `ParseAddressFamily`, `validateInterfaceName`, table-driven unit tests.
- **Step 4 — done (superseded).** First `Service` + `ListInterfaces` via stdlib
  `net`; later replaced by the platform split.
- **Step 5 — done (superseded).** Introduced the source interface + `GetInterface`
  - `get_interface` tool with domain errors; source later moved behind the
    platform split.
- **Step 6 — done.** Platform split: shared `Source` contract; `source_linux.go`
  (netlink, with real operational state) and `source_darwin.go` (stub returning
  `errNotImplemented`); `NewSource` -> `newPlatformSource()`; `Interface` gained
  `OperState`; tests split into shared (fake) + per-OS real-host files;
  `GOOS=darwin` cross-build verified. (Shared code later refactored from a
  `Service` struct to package functions + a pluggable `Source`; the shared file
  is `internal/network/network.go`.)
- **Step 7 — done.** `get_interface_statistics`: `InterfaceStatistics` type,
  `Source.InterfaceStats`, `GetInterfaceStatistics`; Linux via netlink link
  stats; macOS stub. New tool.
- **Step 8 — done (superseded).** `get_default_routes`: `Route` type,
  `Source.DefaultRoutes`, `GetDefaultRoutes`; Linux via netlink `RouteList`;
  macOS stub. Originally kept routes where `Dst == nil`; corrected in Step 13,
  because netlink reports a default route as a non-nil `Dst` with a zero-length
  prefix (`0.0.0.0/0`), so the nil-only filter dropped every default route.
- **Step 9 — done.** In-process MCP protocol test
  (`cmd/netstate-mcp/main_test.go`): server construction extracted into
  `newServer(src)`; test drives `initialize`, `tools/list`, a valid
  `tools/call`, and invalid tool-level and unknown-tool calls over an in-memory
  transport, with a fake `Source` and no LLM.
- **Step 10 — done.** `README.md` (build, tools, MCP Inspector usage, testing,
  layout, design notes). Manual MCP Inspector verification is a user step
  (`npx @modelcontextprotocol/inspector ./bin/netstate-mcp`).
- **Step 11 — done.** Native macOS `darwinSource`:
  `Interfaces` via stdlib `net` (OperState "unknown"); `InterfaceStats` via
  `NET_RT_IFLIST2` (`if_msghdr2`/`if_data64` counters read at struct-derived
  offsets); `DefaultRoutes` via `route.FetchRIB`/`ParseRIB` filtering zero-dst
  gateway routes. Added `golang.org/x/net`; `golang.org/x/sys` now direct.
  `errNotImplemented` removed; darwin tests are real-host integration checks.
- **Step 12 — done (macOS hardware-validated).** Ran the full suite natively on
  macOS 15.7.9 (Intel, go1.27.1): `gofmt`/`vet`/`build`/`test -race` clean; all
  four tools smoke-tested over stdio against live state and cross-checked with
  `netstat`. Found and fixed one real bug: in `parseIfMsghdr2`,
  `unsafe.Offsetof(m.Data.Ibytes)` is relative to the innermost struct
  (`m.Data`), not to the outer message — Go defines Offsetof of a nested field
  within that field's own struct type. Every counter read landed 32 bytes early
  (inside the header): e.g. `tx_packets` returned the MTU, `rx_bytes` 0,
  `rx_dropped` held ibytes. Fix adds `dataBase := unsafe.Offsetof(m.Data)` to
  all seven counter offsets (`m.Index` is a direct member and needs none).
  New regression test `TestDarwinSourceStatsCounters` moves 8 KiB over loopback
  and requires lo0 RX/TX packet+byte counters to reflect it; confirmed it fails
  with the old offsets and passes with the fix. Route output matches
  `netstat -rn` exactly (ipv4 default via gateway/en0; ipv6 utun defaults).
- **Step 13 — done (Linux bugfix + schema portability).** Two fixes verified on
  Linux. (1) `get_default_routes` returned an empty slice even on a host with
  default routes: netlink reports a default route as a non-nil `Dst` with a
  zero-length prefix (`0.0.0.0/0`), not a `nil` `Dst` as the filter assumed, so
  `Dst != nil` dropped every default route. `DefaultRoutes` now uses a pure
  `isDefaultRoute` helper that accepts both `nil` and a /0 prefix; new
  deterministic `TestIsDefaultRoute`. (2) MCP Inspector warned that the
  `routes` (and `interfaces`) output properties used `"type": ["null","array"]`
  (the SDK's slice inference), which some MCP clients reject. The two list tools
  always return a non-nil slice, so they now register an explicit
  `OutputSchema` with a plain `"type": "array"` (built by `outputSchemaForSlice`
  in `cmd/netstate-mcp/main.go`); new protocol test
  `TestSliceOutputSchemasAreNotNullable`.

### Roadmap (optional / later)

- Operational state on macOS via `SIOCGIFMEDIA` if wanted (currently "unknown").
- A `.gitignore` (ignoring `bin/`) and any release polish.
