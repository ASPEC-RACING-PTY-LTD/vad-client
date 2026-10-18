# 02 — Shared packages (`pkg/*`), automation schema and web editor mapping

Scope: `pkg/automation`, `pkg/client`, `pkg/protocol`, `pkg/release`, `pkg/version`, `pkg/pathutil`,
`pkg/mimetypes`, `tests/integration`, and `web/src/automation/*` (only where it mirrors the schema).
Server internals, web pages, desktop internals and OSS comparison are covered by other agents; they are
cited here only where they consume or constrain the shared packages.

All citations are `path:line` against the tree as read on 2026-10-17. `go test ./pkg/...` passes
(all packages `ok`, `pkg/mimetypes` has no tests).

Confidence tags: **[V]** verified by reading code, **[I]** inferred from code but not executed,
**[A]** assumption / external knowledge that needs confirmation.

---

## 1. Current state

### 1.1 Module layout and who compiles what

- Root module `github.com/vaultdock/vaultdock` (`go.mod:1`) owns every `pkg/*` package. Dependencies are
  small: chi, maxminddb, x/crypto, x/sys, x/time, modernc sqlite (`go.mod:5-12`). **[V]**
- `desktop/go.mod:5` has `replace github.com/vaultdock/vaultdock => ../`, so the desktop client compiles
  the *same* `pkg/*` source as the server. Any dependency added to the root module for a new shared
  package (e.g. a zstd library) becomes a dependency of both binaries. **[V]**
- Consumers of `pkg/client`/`pkg/protocol`: `internal/api/{server,devices}.go`, `internal/testserver`,
  `desktop/app.go`, `desktop/internal/engine/{automations,engine,report,transfer}.go`. **[V]**

### 1.2 Automation document schema (`pkg/automation/schema.go`)

- Package doc states the same code runs on the server (validation, dry runs, storage) and on the desktop
  (live evaluation) (`schema.go:1-7`). **[V]**
- Single schema id `vaultdock.automation/v1` (`schema.go:17`). No version field other than this string,
  and no migration code anywhere in the package. **[V]**
- Limits: 200 nodes, 400 edges, name 120, description 2000, rule depth 5, 50 rules per group,
  1024-char strings, 100 list items (`schema.go:20-29`). **[V]**
- Graph model: three node types `trigger|condition|action` (`schema.go:32-36`); ports `next|true|false`
  (`schema.go:39-43`). A document is `{schema,name,description,enabled,priority,nodes,edges}`
  (`schema.go:46-56`); a node is `{id,type,kind,label,params,position}` (`schema.go:59-66`); an edge is
  `{from,to,port}` (`schema.go:77-81`).
- Branching = condition nodes with `true`/`false` ports. AND/OR = the `group` condition with
  `mode: all|any` and nested `rules` (`catalog.go:176-183`), nested up to `MaxRuleDepth`
  (`validate.go:643-645`). There is no explicit "merge"/"join" node; fan-in is just multiple incoming
  edges. **[V]**
- `Parse` decodes **strictly** with `DisallowUnknownFields` and rejects trailing data
  (`schema.go:85-100`). Lines 96-99 are a no-op branch (both paths return `&a, res`). **[V]**
  Consequence: any new top-level or node field is a hard parse error for older binaries (see §6 R1).
- `Clone` is a JSON round trip (`schema.go:128-133`).

### 1.3 Catalogue (`pkg/automation/catalog.go`)

- Contexts: `file`, `sync`, `schedule` (`catalog.go:5-10`). Triggers declare one `Context`, conditions
  and actions declare allowed `Contexts` (`catalog.go:42-57`). **[V]**
- Param types: string, template, string_list, integer, bytes, enum, boolean, date, glob, rules,
  variable (`catalog.go:13-25`). `ParamSpec` carries required/default/options/min/max/description/
  example/templated (`catalog.go:28-40`). **[V]**
- 10 triggers (`catalog.go:70-112`): `file.created|modified|renamed|deleted`, `folder.changed`,
  `schedule.interval` (5 min–7 days, `catalog.go:89-93`), `sync.completed`, `sync.failed`,
  `file.size_threshold`, `file.extension_matched`.
- 9 conditions (`catalog.go:117-184`): name.contains, name.matches, extension.in, size.compare,
  path.matches, file.age, date.compare (file-only); variable.compare and group (all contexts).
- 8 actions (`catalog.go:186-228`): sync.file, sync.skip, dest.set_folder, dest.rename, tag.apply
  (file only); dest.create_folder, sync.start_task, notify.send (all contexts).
- `FileActionKinds` (`catalog.go:231`) is declared but not referenced elsewhere in `pkg/` (grep). **[V]**
- The catalogue is a package-level map built once from three slices by an initialiser closure
  (`catalog.go:233-251`) which also stamps `Type` and `Ports`. There is **no registration API**; adding a
  kind means editing these slices. `Catalog()` returns copies sorted by kind (`catalog.go:262-271`).
- Variables: 28 `VariableSpec`s with contexts (`catalog.go:286-315`), lookup helpers
  (`catalog.go:321-341`). Template filters documented in `TemplateFilters` (`catalog.go:344-357`) and
  implemented in `template.go:28-31,191-219`. **[V]**

### 1.4 Validator (`pkg/automation/validate.go`)

- Returns `{errors, warnings}` with JSON paths and node ids (`validate.go:14-24`). Errors block
  enabling; drafts may be saved disabled (`internal/api/automations.go:71-74`). **[V]**
- Order: header checks (`validate.go:52-78`) → per-node id/type/kind/params (`validate.go:80-120`,
  unknown kinds get a Levenshtein "did you mean" hint, `validate.go:425-462`) → edges: unknown nodes,
  self-loops, incoming to triggers, port legality, duplicates (`validate.go:123-171`) → **global cycle
  detection** by DFS colouring (`validate.go:176-179, 385-423`) → per-trigger reachability walk and
  **context checks** (node contexts, nested group rules, template variables, variable.compare)
  (`validate.go:182-211, 251-320`) → **conflicting-action warnings** per route
  (`validate.go:322-358`) which hard-code `sync.file`/`sync.skip`/`dest.*`/`tag.apply`.
- Parameter normalisation mutates the document in place: defaults filled, extensions lower-cased,
  `"50 MB"` → bytes (`validate.go:464-678`, `ParseBytes` 734-753). Integers come back as `float64`
  (`validate.go:579-590`). **[V]**
- Route enumeration in `checkPathConflicts` is capped at 2000 leaves (`validate.go:328`). **[V]**

### 1.5 Engine (`pkg/automation/engine.go`) — pure plan output

- Event types are file/schedule/sync only (`engine.go:17-25`); `Event` carries config/root/destination/
  device, optional `File`/`Sync` payloads, `TriggerNodeID`, `Now` (`engine.go:52-62`). **[V]**
- `BuildVars` derives the variable map (`engine.go:68-135`).
- `Evaluate` is explicitly pure: "never touches the filesystem or network; the caller applies the
  outcome" (`engine.go:173-175`). It walks from every matching trigger; conditions follow the matching
  port, actions follow `next`; a shared `visited` set means each node runs at most once per evaluation;
  `MaxSteps=1000` (`engine.go:28, 175-244`). **[V]**
- Trigger matching, condition evaluation and action application are three `switch` statements on kind
  strings (`engine.go:274-331, 357-433, 492-559`) — the main code hotspot for new kinds.
- `Outcome` has one **dedicated struct field per action effect** (`Decision`, `DestFolder`, `Tags`,
  `StartTasks`, `Notifications`…) (`engine.go:155-171`). `EvaluateAll` merges outcomes by priority with
  bespoke rules per field (skip beats sync at equal priority, last destination wins, union of tags)
  and fills `RemoteDir/RemoteName` (`engine.go:584-678`). `Plan.Decision` defaults to `"sync"`
  (`engine.go:585`) — the merge model is file-upload-centric. **[V]**

### 1.6 Templates, glob, reference/AI docs, built-ins

- Templates: `{{var|filter:"arg"}}` parser/renderer with 8 filters (`template.go:12-31, 34-66, 167-219`).
  Unknown variables are rejected at validation (`validate.go:680-688`). Rendered paths/names are
  sanitised so templates cannot escape the destination (`engine.go:507-523` via `pathutil.SanitizePath`,
  `pathutil.go:127-138`). **[V]**
- Glob → anchored regexp with `*`, `**`, `**/`, `?`, `[...]`, backslash-as-separator, process-wide cache
  (`glob.go:10-80`). The cache (`sync.Map`) is unbounded **[V]**; it is keyed by pattern text, and
  patterns come only from validated documents, so growth is bounded by stored automations in practice **[I]**.
- `JSONSchema()` (draft 2020-12) is generated from the catalogue (`reference.go:10-70`); `AIGuide()`
  emits a plain-text prompt containing all kinds, params, variables, filters and an example
  (`reference.go:140-216`). The static prose is sync-specific and says "Actions never delete, move or
  rename the user's local files" (`reference.go:145-166`) — will become wrong once retention/prune
  actions exist.
- Five built-in templates (`reference.go:228-316`), all sync/file oriented.

### 1.7 Where automations run

| Function | Server | Desktop |
|---|---|---|
| Reference (catalog, variables, filters, JSON Schema, example, AI guide) | `GET /automations/reference` `internal/api/automations.go:16-26` | — |
| Validate | `POST /automations/validate` `automations.go:41-49`; on save `automations.go:66-74` | re-validates every delivered doc `desktop/internal/engine/automations.go:44-52` |
| Dry run | `POST /automations/dry-run`, 1–100 sample events, single automation `automations.go:206-238` | — |
| Delivery | `GET /device/automations`: server re-parses, silently drops invalid docs (`internal/api/devices.go:300-316`) | fetched + cached for offline use (`engine/automations.go:30-82`) |
| Live evaluation | **none** | file/scan events (`engine/scan.go:192,246,294`), sync completed/failed (`engine/automations.go:222`), schedules (`engine/automations.go:235-264`) |
| Side effects | — | `applySideEffects`: ensure folders, notifications, chained sync tasks with depth/cooldown guards (`engine/automations.go:15-19,131-161`) |

- The server has **no automation runtime, scheduler, run history or approval store**. The `automations`
  table stores `definition TEXT`, `valid`, `enabled`, `priority`, `is_template` and no schema-version or
  target column (`internal/store/migrations.go:157-170`). **[V]**
- `schedule.interval` state lives only in an in-memory map (`desktop/internal/engine/engine.go:82,114`);
  on first sighting it *defers* rather than fires (`engine/automations.go:244-249`). After every app
  restart the interval starts over, so a daily schedule on a PC restarted daily may never fire. **[V]**

### 1.8 Web editor mapping (`web/src/automation/*`)

- The catalog is **served by the server**, not hard-coded: `useReference()` fetches
  `/automations/reference` with `staleTime: Infinity` (`model.ts:64-66`). Palette, param forms,
  variable menus and rule editor are catalog-driven (`ParamEditor.tsx:75-172`). **[V]**
- Hard-coded schema knowledge that will drift:
  - `SCHEMA_ID` constant (`model.ts:5`); TS mirror types of the Go structs (`model.ts:7-62`) including a
    closed union of param types (`model.ts:30`) and a closed `NodeType` union (`model.ts:7`).
  - `summarize()` switch per kind (`model.ts:118-164`) and kind → icon map (`nodes.tsx:7-35`)
    (unknown kinds fall back to a generic icon/description, so new kinds degrade gracefully).
  - `ParamField` switch over param types (`ParamEditor.tsx:89-135`) — a new param type renders nothing
    useful until added.
  - Default new automation starts with `file.created` (`EditorPage.tsx:144`); default `rules` value
    (`model.ts:235`); new-rule default `name.contains` (`ParamEditor.tsx:207`).
  - Dry-run sample event types are a fixed list of file/sync types and the event JSON is hand-built
    (`Modals.tsx:275-296, 325`). `schedule.*` cannot be dry-run because `matchTrigger` also requires
    `TriggerNodeID` (`engine.go:306-307`). **[V]**
- Mapping: `toFlow` turns nodes into React Flow nodes (`type` = node type, auto-layout when positions
  are missing) and edges into handles keyed by port (`model.ts:166-202`); `fromFlow` reverses it,
  rounding positions (`model.ts:204-216`). Context for the param panel is derived client-side by walking
  incoming edges to triggers (`EditorPage.tsx:67-82`). Connection rules in the UI only block self-loops,
  edges into triggers and duplicates (`EditorPage.tsx:211-220`); all other validity comes from the
  debounced server `POST /automations/validate` (`EditorPage.tsx:153-165`). **[V]**
- AI assistance today = "copy the AI guide / JSON Schema into an external assistant, paste the JSON
  back into Import" (`Modals.tsx` ~150-170, 199). No API keys, no server-side LLM calls. **[V]**

---

## 2. `pkg/client` and `pkg/protocol`

### 2.1 Client API surface (`pkg/client/client.go`, `upload.go`)

- Errors: `APIError{Status,Code,Message,RetryAfter,Body}` (`client.go:29-42`); `Retryable` treats
  408/425/429/5xx (except 501/507), 401 `clock_skew|replay`, 422 `chunk_hash_mismatch`, and network
  errors as retryable (`client.go:45-65`). **[V]**
- Connection: `NormalizeURL` forces https except loopback (`client.go:101-127`); `New` supports exact leaf
  certificate pinning for self-signed servers (`client.go:130-160`); `Probe` returns the presented
  fingerprint for TOFU without exchanging data (`client.go:201-246`). Transport: HTTP/2, 256 idle conns,
  2-minute response-header timeout, **no overall timeout unless `Options.Timeout` is set**
  (`client.go:146-159`).
- Clock: server time from `X-VD-Server-Time` used to correct signing timestamps (`client.go:248-263`).
- Signing: every `Call` signs `METHOD, RequestURI, device, ts, nonce, sha256(body)` (`client.go:265-293,
  329-355`). JSON bodies are fully buffered.
- Endpoints wrapped: enroll + poll (`client.go:381-407`), `Me` (advertises `chunk_size`,
  `max_file_bytes`, `max_concurrent_chunks`, `client.go:411-436`), `EnsureFolder`, `Automations`,
  `ReportState`, `ReportTransfers`, `Notify` (`client.go:439-481`).
- **Missing for backup/restore:** no list/stat/download/content API, no snapshot or chunk-index API,
  no capability negotiation. A standalone restore tool cannot be built on today's client. **[V]**

### 2.2 Upload protocol

1. `POST /api/v1/uploads` `{path,size,sha256,modified_at,tags,on_conflict}` → session or immediate
   `complete` (whole-file dedupe / unchanged) (`upload.go:15-34,56-67`).
2. Server-chosen fixed `chunk_size` (env `VAULTDOCK_CHUNK_SIZE_MB`, default 8, clamped 1–64 MiB,
   `internal/config/config.go:141-145`); `Missing()` drives resume (`upload.go:37-54`).
3. `PUT /uploads/{id}/chunks/{i}` streamed; the chunk SHA-256 is computed first and is **the signed body
   hash** (`upload.go:96-121`). Server requires exact length and, for devices, treats the signed hash as
   authoritative (`internal/api/uploads.go:296-311`); 503 + `Retry-After` when the write semaphore is
   saturated (`uploads.go:313-324`).
4. `POST /uploads/{id}/complete` (`upload.go:80-83`).
- `UploadFile` is a sequential reference implementation (`upload.go:137-180`); the desktop has its own
  parallel scheduler on the same primitives (`desktop/internal/engine/transfer.go:306,431-441`).
- Dedupe is **whole-file** SHA-256 only; chunks are positional, not content-addressed, so a one-byte
  insert re-uploads everything after it. **[V]**

### 2.3 Signing format and replay protection (`pkg/protocol/protocol.go`)

- Canonical string: `VD1\nMETHOD\npath?query\ndeviceID\nts_ms\nnonce\nbody_sha256` (`protocol.go:43-53`),
  Ed25519 signature, base64 (`protocol.go:66-81`). Headers `X-VD-Device|Timestamp|Nonce|Content-SHA256|
  Signature`, `X-VD-Server-Time` (`protocol.go:25-30`).
- `Verify` checks format, ±5 min skew, signature (`protocol.go:91-112`). Nonce uniqueness is the
  caller's job (`protocol.go:89-90`): the server keys an **in-memory** `NonceCache` by
  `keyFingerprint|nonce` with TTL 2×skew+1 min (`internal/api/server.go:65,430`,
  `internal/auth/auth.go:246-288`), and rejects anything timestamped before process start
  (`auth.go:276-278`). Body hash is verified against the buffered JSON body; for chunk PUTs (identified
  by a hard-coded path test `isStreamedBody`, `server.go:387-389`) it is handed to the handler.
- Not covered by the signature: other headers (Content-Type, `X-Chunk-SHA256`), and responses (server →
  device integrity relies on TLS/pinning only). Automation definitions delivered to devices are
  therefore not end-to-end signed. **[V]**

### 2.4 Other shared packages

- `pkg/release`: GitHub latest release, SHA-256 from GitHub digest or `SHA256SUMS`, HTTPS/github-only
  download and redirect policy (`release.go:80-205`). Checksums only — **no publisher signature**;
  integrity rests on GitHub account security. **[V]**
- `pkg/version`: semver-ish parse/compare/`Newer` (`version.go:12-77`) — usable for capability gating.
- `pkg/pathutil`: *virtual server path* rules (Windows-reserved names, forbidden chars, trailing
  dot/space, depth 64) and lossy `SanitizeName/SanitizePath` (`pathutil.go:13-138`). It is designed for
  the server tree and must **not** be used to encode backup-manifest paths (it would rewrite legitimate
  local names). **[V]**
- `pkg/mimetypes`: extension table (`mimetypes.go:11-59`), used by `BuildVars` (`engine.go:107`).

### 2.5 Tests

- `pkg/automation/automation_test.go` (14 tests: templates round trip, Medal evaluation, validation
  errors, ports/cycles, contexts, strict fields, conflict warnings, priority merge, templates, glob,
  bytes, destination escape, schema/guide) (`automation_test.go:19-324`). No fuzz tests.
- `pkg/client`: pinning + URL normalisation only (`client_test.go:13-63`); upload logic is exercised
  indirectly by `internal/api/api_test.go:427-494`.
- `pkg/protocol`: one sign/verify test (`protocol_test.go:10`).
- `tests/integration` (build tag `integration`, `integration_test.go:1`) drives a deployed instance in two
  phases around a container restart: setup/auth, device reject/approve, large upload through a
  connection-dropping proxy (`startProxy`, `integration_test.go:172-208`), resume, duplicates, quota,
  isolation, range requests, automation validate/import/export/delivery/dry-run, revocation
  (`integration_test.go:210-397`), persistence checks (`398+`).

---

## 3. Reuse map (requirements 1–10)

| Req | Reusable as-is | Reusable with extension | Not present (new) |
|---|---|---|---|
| 1 Backup engine | `pkg/mimetypes` (preview/classify) | — | discovery, app inventory: desktop-owned |
| **2 Independent jobs, CDC, dedupe, resumable** | `client.Retryable`/`retry` pattern (`client.go:45-65`, `upload.go:182-202`); `HashSection` (`upload.go:86-92`); signed streamed body hash (`upload.go:105-121`); 503 back-pressure contract | `pkg/client` gains chunk-index/pack/snapshot calls; `isStreamedBody` must learn new routes | **pkg/chunker**, **pkg/vdcrypt**, **pkg/pack**, **pkg/snapshot**, **pkg/repo** (§4.7) |
| 3 Recovery | `pkg/pathutil` for *server* paths only | — | **pkg/restore** (profile remap planner), download API in client |
| 4 Error handling | `ValidationResult` issue shape (`validate.go:14-24`) as a model for actionable issues | — | job error taxonomy lives in `pkg/snapshot`/desktop |
| 5 Sync | entire automation file/sync context | rulesets could reuse `CompileGlob`/`MatchGlob` (`glob.go`) and the `group` rule evaluator | — |
| 6 Storage | `client.New` TLS pinning | backends in `pkg/repo` | S3/SMB backends |
| 7 Web mgmt | `JSONSchema`, dry-run concept | retention dry-run = engine "plan" pattern | `pkg/retention` policy evaluator (pure) |
| **8 Automations & AI** | graph model, validator, contexts, templates, catalog-driven editor, reference endpoint, AI guide | new contexts/kinds/param types, generic ops, approval metadata, server runtime, schedule package | approval store + runner (server), AI provider proxy (server) |
| **9 Reliability/security** | Ed25519 device signing, skew/replay, pinning, `release` checksum checks | signed snapshot manifests (reuse `protocol` keys) | client-side encryption, integrity verify, standalone restore |
| 10 UX | AI guide / templates as "first-time setup" recipes | built-in backup templates in `reference.go` | readiness report data model |

---

## 4. Required changes

### 4.1 Prerequisite refactor: make the automation package extensible without hotspots

Today every new kind touches `catalog.go` (slices), `engine.go` (three switches + `Outcome` fields +
merge logic), `validate.go` (`checkPathConflicts`), `reference.go` (prose), and on the web
`model.ts`/`nodes.tsx`. Proposal (one owner, lands first):

1. **Registry** (`pkg/automation/registry.go`, new): `RegisterKind(spec KindSpec, h Handlers)` with
   `Handlers{Match TriggerFunc; Eval ConditionFunc; Apply ActionFunc}`; existing kinds are registered
   from `catalog.go`. Per-domain files (`kinds_backup.go`, …) register in `init()`. `Catalog()` already
   sorts (`catalog.go:262-271`) so init order does not matter. **[I]**
2. **Generic operations**: add `Ops []Operation` to `Outcome` and `Plan`
   (`Operation{Kind, NodeID, Params map[string]any, Target string, Destructive bool,
   Approval ApprovalState, Merge string}`). New actions only emit ops; existing fields stay for v1 kinds
   so desktop behaviour is unchanged. Merge policy comes from `KindSpec.Merge`
   (`union|last_wins|first_wins|exclusive`) instead of bespoke code (`engine.go:596-647`).
3. **Conflict rules as data**: `KindSpec.ConflictsWith []string`, `KindSpec.MaxPerRoute int`
   replace the hard-coded checks in `validate.go:339-350` (keep the old messages for v1 kinds via the
   same data).
4. **KindSpec metadata** (all JSON `omitempty`, visible to web/AI): `Runtime []string`
   (`desktop|server`), `Destructive bool`, `Approval string` (`none|optional|required`), `Icon string`,
   `Summary string` (a template rendered by the web instead of `model.ts:118-164`), `Since string`
   (schema version that introduced it).
5. **AI guide prose** generated per context/runtime, removing the false "never delete" claim
   (`reference.go:162`) once destructive kinds exist.

### 4.2 New contexts

Current contexts conflate "where it runs" and "what data it carries". Proposed:

| Context | Data (variables) | Produced by | Runtime |
|---|---|---|---|
| `file` (existing) | `file.*`, `sync.*` | desktop watcher | desktop |
| `sync` (existing) | `sync.*` run stats | desktop | desktop |
| `schedule` (existing) | `now`, `device.name` | timer | desktop **or** server |
| `backup` (new) | `job.id/name/type/app`, `snapshot.id`, `run.files_*`, `run.bytes_*`, `run.skipped`, `run.warnings`, `run.duration_s`, `run.error` | backup job end | desktop (event), server (on snapshot commit) |
| `verify` (new) | `verify.snapshot`, `verify.checked`, `verify.corrupt`, `verify.missing` | verification run | server or desktop |
| `storage` (new) | `dest.name/type`, `dest.used_bytes`, `dest.free_bytes`, `dest.used_pct`, `quota.*` | periodic probe | server |
| `recovery` (new) | `restore.id`, `restore.status`, `restore.files_failed` | restore runs | desktop |

Keep sync and DR logically separate (Req 5) by never letting `sync`/`file` actions accept the `backup`
context and vice versa (enforced by existing `checkContext`, `validate.go:251-270`).

A document-level `runtime` (`"device"` default, `"server"`) is needed so the validator can reject
mixing a desktop-only trigger with a server-only action in one document, and so the server can decide
which documents to deliver (`devices.go:300-316`) versus run itself.

### 4.3 New trigger kinds

| Kind | Context | Params | Notes |
|---|---|---|---|
| `schedule.cron` | schedule | `cron` (new `PTCron`), `timezone`, `catch_up: skip|run_once`, `jitter_minutes` | needs persisted last-fire (see §1.7 restart bug) |
| `schedule.daily` / `schedule.weekly` | schedule | time, weekdays | UI-friendly sugar over cron |
| `backup.completed` / `backup.failed` / `backup.partial` | backup | `jobs` (new `PTJobRef` list), `job_types` | "partial" = completed with skips (Req 4) |
| `verify.failed` / `verify.completed` | verify | `jobs`, `min_corrupt` | |
| `storage.threshold` | storage | `destination`, `used_pct` or `free_bytes` | server runtime |
| `snapshot.age_exceeded` | schedule | `jobs`, `max_age_hours` | recovery-readiness (Req 10) |
| `device.offline` | schedule | `hours` | server runtime |

### 4.4 New action kinds

| Kind | Destructive | Approval | Runtime | Emits op |
|---|---|---|---|---|
| `backup.run_job` | no | none | desktop (server queues request to device) | `backup.run` |
| `backup.run_all` | no | none | desktop | |
| `retention.apply` (`policy`: keep_last/hourly/daily/weekly/monthly/yearly, `dry_run`) | **yes** | **required** unless `dry_run:true` | server | `retention.plan` + `retention.prune` |
| `replicate.snapshot` (`to_destination`) | no | none | server or desktop | |
| `verify.snapshot` (`depth: metadata|sample|full`, `sample_pct`) | no | none | server/desktop | |
| `recovery.test_restore` (`to_scratch`) | no | optional | desktop | Req 9 recovery testing |
| `recovery.start` | **yes** (overwrites local data) | **required** | desktop | |
| `gc.run` (dedupe-aware garbage collection) | **yes** | **required** | server | |
| `snapshot.protect` / `snapshot.unprotect` | unprotect = **yes** | required for unprotect | server | Req 9 protected points |
| `ai.diagnose` (`question` template) | no | none | server | produces a report, never actions |
| `notify.send` (existing) | no | — | both | extend `channel` with `email|webhook` later |

### 4.5 Approval gates

Requirements: destructive operations must wait for human approval (Req 8), and AI-generated or imported
JSON must not be able to approve itself.

Proposed model:
- **Catalog owns destructiveness**: `KindSpec.Destructive` and `Approval` are catalogue data, not document
  data. A document may only *raise* the bar (`params.approval: "always"` on any action), never lower it.
- **Engine stays pure**: `Evaluate` marks destructive ops `Approval: "pending"` in the plan; it never
  executes or approves. A plan with pending ops is returned to the runner.
- **Approval is a server record**, keyed by `(automation_id, definition_sha256, node_id, op_digest)`,
  created by an authenticated web user (session + CSRF, `internal/api/server.go:455-470`) and
  invalidated whenever the definition hash changes. Optional standing approvals (e.g. "allow retention
  for job X with policy hash Y") are also server records, never document fields.
- **Validator rules** (new `approval.go`): error if a destructive action is reachable from a trigger
  whose runtime cannot reach a human (e.g. desktop offline-only without server); warning listing every
  destructive node; error if `retention.apply` has no keep rule (would delete everything); error if a
  destructive action can be reached from an `ai.*`-produced path without an approval gate.
- Optional explicit graph node: rather than a fourth node type (would touch `validate.go:95-103`,
  edge/port logic, web node types), model the gate as an **action kind `approval.request`** whose `next`
  edge is only followed after approval. In the pure engine this produces an op
  `approval.wait{continuation: node_id}`; the server runner re-enters `Evaluate` from that node on
  approval. Requires a small engine change: `EvaluateFrom(a, ev, nodeID)`. **[I]**

### 4.6 Versioning and migration

- Introduce `vaultdock.automation/v2` (new fields: `runtime`, per-action `approval`, new kinds). Add
  `migrate.go`: `Upgrade(raw []byte) (*Automation, from string, err)` — v1 → v2 is purely additive
  (set `runtime: "device"`). `Parse` accepts both ids; `SchemaID` stays the *write* version.
- Store the schema version and runtime in the `automations` row (server workstream) so the server can
  filter delivery: old desktops parse strictly (`schema.go:88`) and would reject v2 docs; today the
  desktop silently skips invalid docs (`engine/automations.go:47-49`) — acceptable failure mode but must
  be surfaced. Deliver only docs whose kinds the device's `client_version` supports (compare with
  `pkg/version.Compare`, `version.go:47`); `KindSpec.Since` makes that computable.
- Keep v1 output byte-compatible: `Clone`/marshal of a v1 document must not gain new fields
  (`omitempty` everywhere). Golden test required (§9).

### 4.7 New shared packages (one implementation for desktop, server and standalone restore)

All in the root module (desktop gets them through the existing `replace`). Pure Go, no cgo (desktop and
server are cgo-free today: modernc sqlite) **[I]**.

| Package | Responsibility | Key API (sketch) |
|---|---|---|
| `pkg/chunker` | FastCDC content-defined chunking, per-repository keyed gear table so boundaries do not leak content | `type Params{Min,Avg,Max int; Seed [32]byte}`; `New(r io.Reader, p Params) *Chunker`; `(*Chunker).Next(buf []byte) (Chunk{Off int64; Len int; Data []byte}, error)` |
| `pkg/vdcrypt` (avoid shadowing stdlib `crypto`) | repo master key, Argon2id KDF (x/crypto already a dep), AEAD (XChaCha20-Poly1305 or AES-256-GCM), keyed chunk ids (HMAC-SHA256), key wrapping for recovery key / passphrase / device key | `NewRepoKey()`, `WrapKey(k, pass)`, `UnwrapKey`, `ChunkID(k, plain) ID`, `Seal(k, ad, plain) []byte`, `Open(...)`; envelope `version‖nonce‖ciphertext` |
| `pkg/compress` | zstd encode/decode with level presets; "store" for already-compressed types (uses `pkg/mimetypes`) | `Compress(dst, src, level)`, `Decompress` |
| `pkg/pack` | bundle many small encrypted blobs into ~16–64 MiB packs with a trailing encrypted header (cuts request count for S3/HTTP) | `Writer.Add(id, blob)`, `Writer.Finish() (PackID, Header)`, `Reader.Get(id)` |
| `pkg/snapshot` | manifest format: job, snapshot, tree nodes (raw OS path bytes, attributes, mtime, size, chunk list, ADS/VSS metadata), canonical encoding, device signature (reuses Ed25519 from `pkg/protocol`) | `type Snapshot`, `type Tree`, `Encode/Decode`, `Sign(priv)`, `Verify(pub)` |
| `pkg/repo` | repository over a minimal `Backend` (Put/Get/Stat/List/Delete by key); index of chunk→pack; lock files; prune/GC planner (pure) | `type Backend interface`, `Open(b Backend, key)`, `HasChunks(ids) []bool`, `SaveSnapshot`, `PlanPrune(policy) Plan` |
| `pkg/repo/backend/{local,server}` | local dir/SMB path; VaultDock server via `pkg/client` | S3/R2/B2 backend in its own subpackage to isolate the SDK dependency |
| `pkg/retention` | pure GFS policy evaluation (keep-last/hourly/…/protected) → keep/forget sets with reasons; used by automation dry-run and web preview | `Apply(policy, []SnapshotMeta, now) Result` |
| `pkg/schedule` | cron/daily/weekly parsing, next fire, missed-run policy, jitter | `Parse(expr, tz) (Schedule, error)`, `Next(after)`, `Due(last, now, catchUp)` |
| `pkg/restore` | pure restore planner: old→new profile/user mapping, known-folder remap, conflict policy, ACL stripping decisions | `Plan(snapshot, Mapping, Policy) []Step` |
| `cmd/vaultdock-restore` | standalone restore CLI using `pkg/repo` + backends, **no management server needed** (Req 9) | — |

Design constraints:
- Repo key must be recoverable without DPAPI (a Windows reinstall loses DPAPI-protected device keys,
  which the brief says are DPAPI-stored). Wrap the repo key with a user recovery key/passphrase as well.
- Chunk ids must be keyed (HMAC) when encrypted; the server can then verify only *ciphertext* integrity,
  not plaintext. Whole-file SHA-256 dedupe (`upload.go:18`) cannot coexist with client-side encryption
  for backup data — keep the old path for sync, new path for backups (Req 5 separation).
- Manifests must store raw paths; do not run them through `pathutil` (§2.4).

---

## 5. Client/protocol changes

- **Capability negotiation**: extend `Me.Server` (`client.go:424-430`) with `capabilities []string`
  (`repo.v1`, `automation.v2`, `presign.s3`) and `limits` (max pack size). Client side: new
  `pkg/client/capabilities.go` with `Has(cap)`.
- **New endpoints** (server implements; client wraps in new files):
  - `POST /api/v1/repo/{repo}/chunks/has` — batch existence (keyed ids), JSON, signed normally.
  - `PUT /api/v1/repo/{repo}/packs/{id}` — streamed; signed body hash = SHA-256 of the ciphertext pack.
    Requires `isStreamedBody` (`internal/api/server.go:387-389`) to become a route table, not a path test.
  - `POST /api/v1/repo/{repo}/snapshots` — small signed manifest root + signature.
  - `GET` list/download for snapshots, packs, indexes, plus existing file content for sync restores.
  - Job progress/queue reporting (`/device/jobs/...`) analogous to `ReportTransfers` (`client.go:469-471`).
  - Automation runtime: `GET /device/automation-requests` (server-queued `backup.run_job` etc.) and
    `POST /device/automation-results`.
- **Variable/larger chunks**: content-defined chunks are variable by nature; the fixed-chunk session
  (`upload.go:37-39`, server `uploads.go:298-302` exact-length check) is unsuitable. Use packs
  (single PUT ≤ max pack size) rather than raising `VAULTDOCK_CHUNK_SIZE_MB` (clamped to 64 MiB,
  `config.go:145`). Resume granularity = pack; the desktop upload queue persists packs written locally.
- **Signing**: VD1 already binds the body hash, so pack PUTs need no signature change. Two gaps to
  address in a `VD2` (keep `VD1` accepted): include a signed-headers list (e.g. `Content-Type`,
  idempotency key) and add optional **response signing** or signed manifests so restores can verify data
  without trusting the server.
- **Provider-direct uploads (desktop → S3/R2/B2 without proxying bytes through the server)**:
  - Option A — *presigned URLs*: device asks the server (signed VD1 request) for presigned PUTs for pack
    keys; server holds provider credentials. Server cannot see the bytes, so integrity relies on content
    addressing + provider checksum headers (e.g. `x-amz-checksum-sha256`) included in the presign
    **[A: provider support varies; R2/B2 compatibility must be confirmed by the storage agent]**, and on
    later verification jobs.
  - Option B — *user-managed credentials on the device* (Req 6): device talks SigV4 directly; VaultDock
    signing is not involved for data, only for reporting snapshot metadata to the server. Required anyway
    for "restore without the management server".
  - Both keep `pkg/repo` backend-agnostic; the `server` backend becomes just one `Backend`.
- **Replay protection** is per-process memory (`auth.go:246-288`): fine for one server instance; a
  restart forces clients to retry (they do: `Retryable` on `replay`, `client.go:54`). Long-running pack
  uploads are not affected because the timestamp is checked at request start. **[I]**
- **Client timeouts**: add per-operation contexts for large pack uploads; `ResponseHeaderTimeout` of
  2 minutes (`client.go:154`) may be too short if the server verifies a 64 MiB pack before replying **[I]**.

---

## 6. Limitations and risks

| # | Finding | Evidence | Conf. |
|---|---|---|---|
| R1 | Strict parsing: new fields/kinds make v2 documents invalid on old desktops; they are silently skipped there and on the server delivery path | `schema.go:88`; `engine/automations.go:47-49`; `devices.go:308-311` | V |
| R2 | No server-side automation runtime, scheduler, run log or approval storage; "server-side scheduled" automations need new server code | §1.7, `migrations.go:157-170` | V |
| R3 | `schedule.interval` state is in memory and defers on first sighting → long intervals may never fire on frequently restarted PCs | `engine.go:82,114`; `engine/automations.go:244-249` | V |
| R4 | Engine merge model is upload-centric (`Decision` defaults to `sync`, field-per-effect) → every new action needs struct + merge edits unless generic ops are introduced | `engine.go:155-171,584-678` | V |
| R5 | Kind dispatch is three switches + validator conflict rules + web summary/icon switches → merge-conflict hotspots for parallel workstreams | `engine.go:274-559`; `validate.go:339-350`; `model.ts:118-164`; `nodes.tsx:7-35` | V |
| R6 | AI guide prose asserts no destructive actions; would mislead an LLM once retention/GC exist | `reference.go:162` | V |
| R7 | Dry-run cannot exercise schedule triggers (needs `TriggerNodeID`), and the UI hard-codes event types | `engine.go:306-307`; `Modals.tsx:325` | V |
| R8 | Upload dedupe is whole-file; positional fixed chunks → no CDC, no cross-file dedupe; incompatible with client-side encryption dedupe | `upload.go:15-54`; `uploads.go:298-302` | V |
| R9 | No download/list API in `pkg/client` → standalone restore impossible today | `client.go:357-481` | V |
| R10 | Responses and automation definitions are not signed; trust in server for restores | `protocol.go`; `client.go:295-327` | V |
| R11 | Nonce cache is in-memory and single-instance; HA/multi-replica would allow replays across replicas | `auth.go:246-288` | V (HA not a current goal) |
| R12 | Signed path is `RequestURI` on both sides; a reverse proxy that rewrites path prefixes breaks signatures | `client.go:271,282`; `server.go:412` | I |
| R13 | Releases verified by SHA-256 from GitHub only (no publisher signature); a standalone restore tool distributed the same way inherits this | `release.go:123-160` | V |
| R14 | Adding zstd/S3 SDKs to root `go.mod` grows both server and desktop binaries and their licence surface | `go.mod`, `desktop/go.mod:5` | V |
| R15 | `pathutil.SanitizeName` is lossy; misuse in backup manifests would corrupt names on restore | `pathutil.go:68-90` | V |
| R16 | DPAPI-held device key does not survive a Windows reinstall; any encryption keyed only to it breaks guided restore after reinstall | brief "Ed25519 per-device keys (DPAPI)" | A (desktop agent to confirm) |
| R17 | `glob` regexp cache is unbounded | `glob.go:10,69` | V (low impact) |
| R18 | Integers normalise to `float64`; values >2^53 (byte counts) are rejected by bytes check but integer params have no such guard | `validate.go:579-610` | V (low) |

---

## 7. Implementation workstreams with exclusive file ownership

Rule: a file appears under exactly one workstream. "New" = created by that workstream only.
Ordering: **WS-A1 must merge first** (it removes the hotspots); everything else can then proceed in
parallel because each adds new files only.

### WS-A1 — Automation core refactor (owner: automation-core) — first
Modify: `pkg/automation/schema.go`, `pkg/automation/catalog.go`, `pkg/automation/engine.go`,
`pkg/automation/validate.go`, `pkg/automation/reference.go`, `pkg/automation/automation_test.go`.
New: `pkg/automation/registry.go` (RegisterKind/RegisterVariable/handler maps),
`pkg/automation/ops.go` (Operation, merge policies), `pkg/automation/migrate.go` (v1→v2, `Upgrade`),
`pkg/automation/approval.go` (approval metadata + validator rules), `pkg/automation/runtime.go`
(document runtime, `KindsSupportedBy(version)`), `pkg/automation/testdata/catalog_v1.golden.json`,
`pkg/automation/migrate_test.go`, `pkg/automation/fuzz_test.go`.
Deliverable: existing behaviour byte-identical for v1 docs; extension points for B/C.

### WS-A2 — Backup/verify/storage/recovery automation kinds (owner: automation-backup)
New only: `pkg/automation/kinds_backup.go`, `pkg/automation/kinds_retention.go`,
`pkg/automation/kinds_storage.go`, `pkg/automation/kinds_recovery.go`,
`pkg/automation/kinds_schedule.go` (cron/daily/weekly triggers using `pkg/schedule`),
`pkg/automation/templates_backup.go` (built-in backup templates registered into the template list via a
registration hook added by A1), `pkg/automation/kinds_backup_test.go`.

### WS-A3 — AI assistance data (owner: automation-ai)
New only: `pkg/automation/aitools.go` (tool/function schemas for "propose", "validate", "dry_run",
"explain_plan"; redaction helper that strips `file.path`/device names before sending context to a
user-supplied LLM), `pkg/automation/aitools_test.go`. Provider calls and key storage stay in server
`internal/*` (other agent).

### WS-S — Scheduling (owner: shared-schedule)
New: `pkg/schedule/schedule.go`, `pkg/schedule/cron.go`, `pkg/schedule/schedule_test.go`.

### WS-R — Repository format (owner: shared-repo); can split internally by package
New: `pkg/chunker/*`, `pkg/vdcrypt/*`, `pkg/compress/*`, `pkg/pack/*`, `pkg/snapshot/*`, `pkg/repo/*`,
`pkg/repo/backend/local/*`, `pkg/repo/backend/server/*`, `pkg/retention/*`, plus their `_test.go`.
Modify: `go.mod`, `go.sum` (sole owner of root module dependency changes; other workstreams request
deps through this owner).
S3 backend `pkg/repo/backend/s3/*` is a separate sub-owner (WS-R-S3) only if the storage research picks
an SDK; still new files only.

### WS-RS — Restore planner and standalone tool (owner: shared-restore)
New: `pkg/restore/*`, `cmd/vaultdock-restore/*`.

### WS-C — Client and protocol (owner: shared-client)
Modify: `pkg/client/client.go` (only: `Me.Server` capabilities/limits fields, `Retryable` codes for new
endpoints), `pkg/protocol/protocol.go` (VD2 signed-headers variant, keeping VD1),
`pkg/protocol/protocol_test.go`, `pkg/client/client_test.go`.
New: `pkg/client/capabilities.go`, `pkg/client/repo.go` (chunks/has, packs, snapshots, indexes),
`pkg/client/download.go` (list/stat/download/range), `pkg/client/jobs.go` (job progress + automation
requests/results), `pkg/client/presign.go`, `pkg/client/repo_test.go`.
Untouched: `pkg/client/upload.go` (legacy sync path stays stable).

### WS-W — Web automation editor (owner: web-automation) — single owner for all of `web/src/automation/*`
Modify: `web/src/automation/model.ts` (types for new KindSpec metadata, param types, `runtime`,
v2 `SCHEMA_ID`, catalog-driven `summarize` via `spec.summary`), `web/src/automation/nodes.tsx`
(`spec.icon`, destructive/approval badge), `web/src/automation/ParamEditor.tsx` (cron, duration, job
picker, destination picker, retention-policy editor), `web/src/automation/EditorPage.tsx` (runtime
selector, default trigger from catalog, client-side mixed-runtime hint), `web/src/automation/Modals.tsx`
(dry-run event builder driven by catalog contexts incl. schedule/backup/storage samples; retention
dry-run preview).
New: `web/src/automation/ApprovalBadge.tsx`, `web/src/automation/RetentionPolicyEditor.tsx`,
`web/src/automation/model.test.ts` (requires a test runner; coordinate with web agent who owns
`web/package.json`).

### WS-T — Integration tests (owner: shared-tests)
New: `tests/integration/backup_test.go`, `tests/integration/automation_v2_test.go` (same package; reuse
`newWeb`, `enroll`, `startProxy` from `integration_test.go:70-208` without modifying it).
Modify: none (`integration_test.go` stays as-is; if a helper must change, WS-T is its sole owner).

### Not owned here (interfaces only)
`internal/*` (server agent), `desktop/*` (desktop agent), `web/src/pages/*` and `web/package.json`
(web agent). `pkg/release`, `pkg/version`, `pkg/pathutil`, `pkg/mimetypes`: no changes proposed.

### Hotspot summary

| Hotspot | Why | Mitigation |
|---|---|---|
| `catalog.go` | all kinds in three slices | A1 adds registry; A2 kinds live in new files |
| `engine.go` | three switches + `Outcome` fields + merge | A1 converts to handler maps + generic `Ops` |
| `validate.go` | hard-coded conflict rules | A1 makes conflicts data-driven; approval rules in `approval.go` |
| `reference.go` | static prose + built-in templates | A1 makes templates registrable and prose context-aware |
| `model.ts` | TS mirror + summarize switch | WS-W single owner; summaries/icons move to catalog data so future kinds need no TS change |
| `go.mod` | every new dependency | WS-R sole owner |

---

## 8. Interfaces

### Provided by shared packages
- **To server**: `automation.Parse/Upgrade/Validate`, `EvaluateAll` with `Plan.Ops` + approval states,
  `EvaluateFrom` (resume after approval), `KindsSupportedBy(clientVersion)` for delivery filtering,
  `Catalog()` incl. runtime/destructive metadata, `JSONSchema()`/`AIGuide()` v2, `aitools` schemas;
  `schedule.Next/Due`; `retention.Apply` for previews; `repo.PlanPrune` for GC dry runs;
  `snapshot.Verify` for server-side integrity checks; `protocol.Verify` (VD1+VD2).
- **To desktop**: same automation APIs + new event constructors for backup/verify/recovery contexts;
  `chunker`, `compress`, `vdcrypt`, `pack`, `snapshot`, `repo` + backends; `client` repo/jobs/download
  APIs; `schedule` with persisted-state inputs (caller stores last-fire).
- **To web**: reference JSON gains `runtime`, `destructive`, `approval`, `icon`, `summary`, `since` per
  kind; new param types `cron`, `duration`, `job_ref`, `destination_ref`, `retention_policy`;
  validation issues unchanged in shape (`validate.go:14-24`).
- **To standalone restore**: `repo`, `snapshot`, `vdcrypt`, `restore`, `client/download` (optional
  server backend).

### Needed from server (other agent)
- `automations` table: `schema_version`, `runtime`, `definition_sha256`; tables for automation runs,
  pending approvals, standing approvals; server automation runner + scheduler; device request queue.
- Delivery filter in `handleDeviceAutomations` (`devices.go:300-316`) using client version.
- Reference endpoint unchanged in path (`automations.go:16-26`), richer payload.
- Dry-run endpoint accepting new event payloads and `runtime`; retention/GC preview endpoints.
- Repo endpoints (§5), `isStreamedBody` generalised (`server.go:387-389`), capabilities in `/device/me`.
- AI provider proxy holding user-provided keys; it calls `aitools` and must never auto-approve.

### Needed from desktop (other agent)
- Emit `backup.*`, `verify.*`, `recovery.*` events; apply `Plan.Ops` (run job, verify, test restore);
  persist schedule last-fire in `desktop/internal/state`; surface skipped/invalid automations instead of
  silently dropping (`engine/automations.go:47-49`); report job progress via new client APIs.
- Key management: repo key wrapping with recovery key (not DPAPI-only).

### Needed from web pages (other agent)
- Approvals inbox page, run history page, retention/GC preview pages consuming server endpoints.

---

## 9. Testing plan

1. **Compatibility goldens** (A1): marshal `Catalog()`, `JSONSchema()`, every built-in template and a
   corpus of v1 documents to `testdata/*.golden.json`; v1 docs must parse, validate and marshal
   byte-identically after the refactor; `Upgrade(v1)` is idempotent and `Upgrade(Upgrade(x)) == Upgrade(x)`.
2. **Old-client contract test**: v2 doc with a new kind fails v1-only `Parse` with a clear error, and
   `KindsSupportedBy(oldVersion)` excludes it (guards R1).
3. **Engine equivalence**: table tests that run every existing `automation_test.go` case through the
   new handler registry; `Plan` for v1 kinds unchanged; generic `Ops` merge policies unit-tested.
4. **Approval tests**: destructive op always `pending` regardless of document content; document cannot
   set approval to granted; approval invalidated by definition hash change; `retention.apply` without
   keep rules is an error; `EvaluateFrom` resumes only from `approval.request` nodes.
5. **Fuzzing** (`go test -fuzz`): `Parse`, `ParseTemplate`, `CompileGlob`, `ParseBytes`,
   `protocol.Verify`, `snapshot.Decode`, `pack.Reader`, `schedule.Parse` — no panics, bounded time.
6. **Chunker properties**: deterministic boundaries for a given seed; insertion of N bytes changes only
   O(1) chunks; min/avg/max respected; different seeds give different boundaries.
7. **Crypto**: round trip, tamper detection (bit flip in nonce/ciphertext/AD), wrong key, key wrapping
   with passphrase + recovery key, known-answer vectors pinned in testdata.
8. **Repo**: in-memory and local backends; crash mid-pack (resume), duplicate pack upload idempotent,
   prune plan never deletes chunks referenced by a protected or retained snapshot (property test with
   random snapshot graphs).
9. **Schedule**: DST transitions, missed-run catch-up policies, restart (fed persisted last-fire) — would
   have caught R3.
10. **Client**: `httptest` server tests for each new endpoint, retry on 503/`replay`, pack PUT signs the
    ciphertext hash, capability negotiation fallback to legacy upload path.
11. **Web** (`model.test.ts`): `toFlow`→`fromFlow` round trip preserves documents; unknown kinds render
    with generic icon/summary; approval badge shown for `destructive` kinds.
12. **Integration** (`tests/integration`, tag `integration`): v2 automation create/validate/deliver to a
    device with old and new `client_version`; backup a generated tree through `startProxy`
    (connection drops) and verify resumable pack upload + snapshot commit; restart phase verifies
    snapshot list and restore of a sample file via `pkg/client/download.go`; standalone
    `cmd/vaultdock-restore` restores from a local backend with the server stopped (Req 9).
13. **Desktop cross-module build** in CI: `cd desktop && go build ./... && go test ./...` after every
    `pkg/*` change, since desktop compiles root packages through `replace`.
