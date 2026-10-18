# P0-28 (DSK-A) Named-pipe IPC (go-winio, SDDL, caller SID) + client; main.go agent connect; ProgramData paths; machine DPAPI
Status: completed

## Goal
- Carry `agent.API` over `\\.\pipe\VaultDock.Agent.v1`:
  - length-prefixed JSON-RPC with a version handshake;
  - server-pushed events;
  - a restrictive SDDL;
  - a per-connection caller identity in ctx;
  - a reconnecting client.
- Add ProgramData path helpers and machine-scope DPAPI.
- Make the UI process (`main.go`) connect to the agent over IPC, with an in-process fallback.

## Changes (repo: path, created/modified/deleted, why)
All changes are in the client repo `VaultDock`.

**`desktop/internal/ipc/` (all created)**
- `caller/caller.go`: leaf package with `Caller`, `With` and `From`. It is separate so that `internal/agent` can import it without an import cycle (ipc imports agent).
- `protocol.go`:
  - package doc with the wire spec;
  - `PipeName`, `DefaultMaxFrame`, `Frame`;
  - the length-prefixed codec and `ErrFrameTooLarge`;
  - `CallerFromContext`.
- `methods.go`: the generic handler table that maps wire method names 1:1 to `agent.API` methods.
- `server.go`, the `Server`:
  - handshake: hello, version check, identify, welcome;
  - per-connection ctx carrying the Caller;
  - optional `Authorize` hook;
  - in-flight limit, cancel frames, max subscriptions and write deadlines;
  - subscriptions bound to the connection.
- `client.go`, the `Client` (implements `agent.API`):
  - lazy dial with exponential backoff;
  - a reconnect loop that re-subscribes live subscriptions under the same id;
  - drop-oldest event buffers;
  - ctx-bound subscriptions and cancel frames.
- `pipe_windows.go`:
  - `PipeSDDL`, `Listen` (go-winio, byte mode) and `DialPipe` (identification impersonation level);
  - `identifyConn`: `GetNamedPipeClientProcessId`, then `ImpersonateNamedPipeClient` on a locked thread, then `OpenThreadToken` and `RevertToSelf`; the token gives the SID, session, admin, elevated and system flags;
  - `RequireSystemServer` (anti-squatting check on the client side).
- `pipe_other.go`: non-Windows stubs, so the package compiles and the protocol tests run anywhere.
- `ipc_test.go`: portable tests over `net.Pipe` against a real `agent.Host`:
  - round trips;
  - caller ctx and Authorize;
  - events;
  - subscriptions cancelled on connection close and on ctx end;
  - reconnect plus re-subscribe;
  - backoff while unreachable;
  - version mismatch and unidentifiable caller refused;
  - frame limits (codec, server drops an oversized header, client refuses an oversized request and the connection survives);
  - cancelled call, and use after Close.
- `pipe_windows_test.go`: a real pipe with a random name:
  - a second `Listen` on the name fails;
  - the caller SID and session equal the current process's;
  - snapshot events arrive;
  - `RequireSystemServer` rejects a user-owned server;
  - an anonymous-level client is refused.

**`desktop/internal/platform/`**
- `machine.go` (created):
  - `EnvDataDir` (`VAULTDOCK_DATA_DIR`) and `MachineDataDir`;
  - `StateDir`, `KeysDir`, `MachineLogDir`, `ConfigDir`, `SpoolDir` and `EnsureMachineDirs`;
  - `ProtectMachine` and `UnprotectMachine`.
- `platform_windows.go` (modified):
  - DPAPI is refactored into `dpapiProtect`/`dpapiUnprotect(data, entropy, flags)`;
  - per-user `Protect`/`Unprotect` are unchanged in behaviour and format (same entropy, flags 0);
  - adds `protectMachine`/`unprotectMachine` (`CRYPTPROTECT_LOCAL_MACHINE`, optional entropy where nil means none);
  - adds `programDataDir` (`KnownFolderPath(FOLDERID_ProgramData)`, then `%ProgramData%`, then `C:\ProgramData`).
- `platform_other.go` (modified): stubs for the above.
- `machine_test.go` (created): the env override, every subdirectory, `EnsureMachineDirs`, and a default path that is absolute and not under AppData.
- `dpapi_windows_test.go` (created): user, machine-without-entropy and machine-with-entropy round trips; wrong or missing entropy fails.

**`desktop/main.go` (modified)**
- `connectAgent(log)` dials the pipe with a 2s timeout and logs `agent mode: service` or `agent mode: in-process` (with the reason).
  - `RequireSystemServer` is enforced unless `VAULTDOCK_DATA_DIR` is set (dev agent running as the user).
  - The client is closed on exit.
- `newInProcAgent(ctx, log, quit)` builds `agent.NewHost` with the per-user hooks, then `Start`, then returns `agent.NewInProc(h)` and a stop function. It is ready for P0-29 but not yet called (see Gaps).
- The app still builds and runs exactly as before (legacy `App` owns the engine).

**`desktop/go.mod` / `go.sum` (modified)**
- Adds the direct requirement `github.com/Microsoft/go-winio v0.6.4`, which was v0.6.2 indirectly before.

## Interfaces exposed
```go
package caller // …/desktop/internal/ipc/caller
type Caller struct {
	SID       string `json:"sid"`
	SessionID uint32 `json:"session_id"`
	PID       uint32 `json:"pid"`      // informational only
	Admin     bool   `json:"admin"`    // effective member of BUILTIN\Administrators
	Elevated  bool   `json:"elevated"`
	System    bool   `json:"system"`   // S-1-5-18
}
func With(ctx context.Context, c Caller) context.Context
func From(ctx context.Context) (Caller, bool)

package ipc // …/desktop/internal/ipc
const PipeName = `\\.\pipe\VaultDock.Agent.v1`
const DefaultMaxFrame = 4 << 20
const PipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;0x12019b;;;IU)" // "" off Windows
type Caller = caller.Caller
func CallerFromContext(ctx context.Context) (Caller, bool)
var ErrFrameTooLarge, ErrServerClosed error

type ServerOptions struct {
	Log *slog.Logger; MaxFrame int
	Identify  func(conn net.Conn) (Caller, error)                         // default: pipe client token
	Authorize func(ctx context.Context, c Caller, method string) error    // nil = allow all
	MaxSubscriptions, MaxInflight int                                     // 16, 32
	HelloTimeout, WriteTimeout time.Duration                              // 10s, 10s
}
func NewServer(api agent.API, opts ServerOptions) *Server
func (s *Server) Serve(l net.Listener) error // ErrServerClosed after Close
func (s *Server) ServeConn(conn net.Conn)
func (s *Server) Close() error

type ClientOptions struct {
	Dial func(ctx context.Context) (net.Conn, error) // default DialPipe(ctx, PipeName)
	VerifyServer func(conn net.Conn) error            // e.g. RequireSystemServer
	Log *slog.Logger; MaxFrame int
	DialTimeout, WriteTimeout, MinBackoff, MaxBackoff time.Duration // 3s, 10s, 250ms, 10s
	EventBuffer int                                                 // agent.DefaultBuffer
}
func NewClient(opts ClientOptions) *Client               // lazy connect
func Dial(ctx context.Context, opts ClientOptions) (*Client, error) // connected or *agent.Error (unavailable / invalid)
func (c *Client) Close() error
// *Client implements agent.API.

func Listen(name string) (net.Listener, error)              // Windows: go-winio + PipeSDDL
func DialPipe(ctx context.Context, name string) (net.Conn, error)
func RequireSystemServer(conn net.Conn) error

package platform
const EnvDataDir = "VAULTDOCK_DATA_DIR"
func MachineDataDir() string // %ProgramData%\VaultDock or $VAULTDOCK_DATA_DIR
func StateDir() string; func KeysDir() string; func MachineLogDir() string; func ConfigDir() string; func SpoolDir() string
func EnsureMachineDirs() error
func ProtectMachine(data, entropy []byte) ([]byte, error)   // CRYPTPROTECT_LOCAL_MACHINE; entropy nil = none
func UnprotectMachine(data, entropy []byte) ([]byte, error)
```

### Wire framing spec (protocol version = `agent.ProtocolVersion` = 1)
**Framing**
- Each frame is a 4-byte big-endian length `n` followed by `n` bytes of JSON (one `Frame`).
- `n == 0` or `n > MaxFrame` (default 4 MiB) is a protocol error and the connection is closed. The size is checked before the body is allocated.
- Pipe byte mode is used; the length prefix does the framing, so message mode adds nothing.

**`Frame` fields**

| Field | Meaning |
|---|---|
| `t` | Frame type |
| `id` | Request id, or subscription id for `event`/`sub_end` |
| `v` | Protocol version |
| `m` | Method name |
| `p` | Params: the method's single argument as JSON; omitted for none |
| `r` | Result: the single result; omitted for error-only methods |
| `e` | `agent.Error` `{code,message}` |
| `ev` | `agent.Event`, forwarded verbatim |
| `d` | Server-side dropped-event count |

**Sequence**
1. Client sends `{"t":"hello","v":1}`.
2. Server replies `{"t":"welcome","v":1}`. On a version mismatch or an unidentifiable caller, the welcome carries `"e"` (code `invalid`) and the server closes. The hello must arrive within 10s.
3. Client sends `{"t":"req","id":N,"m":"<agent.API method>","p":…}`.
4. Server replies `{"t":"res","id":N,"r":…}` or `{"t":"res","id":N,"e":{…}}`. Requests run concurrently (max 32 in flight per connection); responses can arrive out of order.
5. Client may send `{"t":"cancel","id":N}`, which cancels that call's ctx on the server.

**Subscriptions**
- Subscribe: `{"t":"req","m":"Subscribe","p":{"sub":S,"kinds":[…]}}`. The client chooses the subscription id `S` (non-zero, unique per connection, max 16).
- Events: `{"t":"event","id":S,"ev":{seq,kind,time,data},"d":dropped}`.
- `{"t":"sub_end","id":S}` is sent when the server-side subscription ends by itself (the host bus closed).
- Unsubscribe: `{"t":"req","m":"Unsubscribe","p":S}`.
- Server subscriptions use the connection ctx, so all of them are closed when the connection ends.

**Client behaviour**
- Subscriptions persist across reconnects: after a redial the client re-sends Subscribe with the same `S`.
- `Dropped()` is the server-side drops plus local drops.
- `sub_end` does not close the client channel. The subscription is re-established when the client reconnects to a restarted agent, and it ends only on `Close`, ctx end or `Client.Close`.

### SDDL choice
`D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;0x12019b;;;IU)`

| Part | Meaning |
|---|---|
| `D:P` | Protected DACL |
| `(D;;GA;;;NU)` | Deny ACE for NETWORK logons. go-winio also sets `FILE_PIPE_REJECT_REMOTE_CLIENTS`. |
| `(A;;GA;;;SY)`, `(A;;GA;;;BA)` | Full access for SYSTEM and Administrators |
| `(A;;GA;;;OW)` | Owner Rights: the creating process can add further pipe instances. In the service that is SYSTEM; in tests it is the test user. |
| `(A;;0x12019b;;;IU)` | Interactive users (console and RDP) get `FILE_GENERIC_READ | FILE_GENERIC_WRITE` minus `FILE_APPEND_DATA` (= `FILE_CREATE_PIPE_INSTANCE`), so a normal user cannot add a rogue server instance. |

- The client therefore dials with `GENERIC_READ | FILE_WRITE_DATA` (not `GENERIC_WRITE`) at `SECURITY_IDENTIFICATION`.
- `Listen` uses `FILE_CREATE` on the first instance, so it fails if the name is already taken. This was verified in the test.
- On the client side, `RequireSystemServer` rejects a squatting server: it requires the server token to be S-1-5-18. A non-admin cannot open a SYSTEM process token, so the fallback is that the server process must be in session 0.

### Caller-context API
- The server derives the caller from the client's **token** (impersonation at identification level after reading the hello frame). The PID is used only for logging.
- An anonymous-level client (`OpenThreadToken` fails) is refused at handshake.
- Every call's ctx carries the caller. Read it with `ipc.CallerFromContext(ctx)` or `caller.From(ctx)`.
- `ServerOptions.Authorize(ctx, caller, method)` is the hook for per-method policy: read-only for any interactive user, admin for machine-wide or destructive calls (§7.6).

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go get github.com/Microsoft/go-winio@latest` gave v0.6.4. LICENSE in the module cache is MIT (Copyright 2015 Microsoft). Its own dependency `sirupsen/logrus` is MIT.
- `gofmt -l main.go internal/ipc internal/platform`: clean.
- `go build ./...`: ok. An earlier attempt failed in `sdk/client` while another task was mid-edit. That resolved by itself; it was not my file.
- `go vet . ./internal/ipc/... ./internal/platform/`: ok.
- `go test ./internal/ipc/... ./internal/platform/ ./internal/agent/`: ok. `ipc` was repeated with `-count=5`: all 11 tests pass every run (real pipe included).
- `GOOS=linux go vet ./internal/ipc/... ./internal/platform/`: ok. The `net.Pipe` tests compile for Linux; they were run on Windows only, since there is no Linux host here.
- `cd e2e && go test ./...`: ok (15.3s).
- `-race` was not run (no cgo on this host).

## Contract issues
None. IPC is internal and not part of `api-v1.md`.

## Gaps / follow-ups
- **The UI does not yet use the API object.**
  - `main.go` connects and logs the mode. In service mode it holds the IPC client, but the legacy `App` still runs its own per-user engine; a warning is logged.
  - In-process mode is still the legacy `App`. `newInProcAgent` (Host + `NewInProc`) is ready but not called: a second Host on the same `DataDir` would race App's `state.db`, as P0-26 warned.
  - **P0-29** wires `NewApp(log, ag.api or newInProcAgent(...))` and removes `state.Open`/`engine.New` from `main.go`. `main.go` is P0-28-owned, so P0-29 needs ownership of it or INT must hand it over.
- **No authorisation policy yet.** `ServerOptions.Authorize` is nil by default (allow all). The service (P0-27) or the profiles task should supply the §7.6 policy.
- **Components and the SDDL:** components started under S4U or user tokens (`vd-discovery`) may lack the INTERACTIVE SID, so they would be denied by the IU ACE. Revisit with a component pipe or an added ACE when components land.
- **`RequireSystemServer` fallback:** when the client cannot open the server token, the check reduces to "server is in session 0" (any service).
- **`go.mod`:** INT should run `go mod tidy`. `go.sum` only gained the go-winio v0.6.4 lines.

## Notes for dependent tasks / INT
- **P0-27 (service):**
  1. `l, _ := ipc.Listen(ipc.PipeName)`
  2. `srv := ipc.NewServer(host, ipc.ServerOptions{Log: log, Authorize: …})`
  3. `go srv.Serve(l)`
  4. On stop, call `srv.Close()` **before** `host.Close()`. Otherwise clients first get `sub_end`, then a disconnect; both are handled.
  - Use `platform.EnsureMachineDirs()` with `StateDir()` as `agent.Options.DataDir` (or `MachineDataDir()`, per your layout).
  - For the key, a `KeyStore` backed by `platform.ProtectMachine(key, entropy)` under `KeysDir()`, with the ACL set by the installer.
- **Agent authorisation:** `internal/agent` can import `internal/ipc/caller` (leaf, no cycle) to read the caller inside `Host` methods. In-process calls carry no caller.
- **Dev setup:** set `VAULTDOCK_DATA_DIR` for a dev agent running as the user. It also disables the client's `RequireSystemServer` check.
- **New dependency:** `github.com/Microsoft/go-winio` v0.6.4, MIT (licence checked in the module cache). Its transitive dependency `github.com/sirupsen/logrus` is also MIT.
