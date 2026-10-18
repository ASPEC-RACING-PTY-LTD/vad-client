# P1-07 SDK-REPO: repo (init/open, config mac, key files, chain, index, snapshots, Backend)
Status: completed

## Goal
Package `sdk/repo` per backup-format-v1 rev 1.2 §2, §4, §5, §7.4, §10–§13:
- the `Backend` interface, plus in-memory and local-directory backends;
- `Init` (DK, RMK_1, recovery secret, chain_1 and metachain_1, key files, config with mac);
- `Open` with a device, recovery, passphrase or escrow credential; a metadata-only open walks the meta-chain;
- rotation and adding/removing recipients;
- the in-memory index (§7.4) with dedup lookup, and index object writing;
- snapshot save/list/load/verify;
- vector 9 and the index half of vector 5.

## Changes (repo VaultDock; all created)
| File | Contents |
|---|---|
| `sdk/repo/backend.go` | Package doc, `Kind`, `ConfigOID`, `ObjectInfo`, `LockKind`/`Lock`, `Backend`, `IsOID`/`CheckKey`/`CheckObject`, and `LockTable`, a process-local lock manager embedded by both test backends. |
| `sdk/repo/membackend.go` | `MemBackend`, with `Raw`/`SetRaw` for tamper tests. |
| `sdk/repo/dirbackend.go` | `DirBackend`: the exact §2 layout (`config`, `<kind>/<oid>`, `data/<oid[0:2]>/<oid>`), atomic temp-file-and-rename writes, and oid validation so paths cannot traverse. |
| `sdk/repo/config.go` | The §5 `Config`, `NewConfig`, `Encode` (sets the mac), `ParseConfig` (format, version and documented fields), `ConfigMACInput`/`ConfigMAC`/`VerifyConfig` (constant-time). |
| `sdk/repo/recipients.go` | `Recipient` with constructors, and `Credential` with constructors. |
| `sdk/repo/repo.go` | `Repo`, `Options`, `InitOptions`, `Init`, `Open`, accessors, key file and chain loading. |
| `sdk/repo/rotate.go` | `Rotate`, `AddRecipient`, `RemoveRecipient`, `SetEscrowMode`, `Recipients`. |
| `sdk/repo/index.go` | Index JSON types, encode/decode/seal/open, the in-memory `Index`, and `LoadIndex`/`NewPackWriter`/`SavePack`/`FlushIndex`/`WriteIndex`. |
| `sdk/repo/snapshots.go` | `ReadBlob` (ranged read; metadata-only path), `ReadTree`, `SaveSnapshot`, `ListSnapshots`, `LoadSnapshot`, `DeviceSignKey`, `VerifySnapshot`. |
| `sdk/repo/lock.go` | §10 sealed lock objects (`SealLock`/`OpenLock`), for offline tooling only. |
| Tests | `repo_test.go`, `backend_test.go`, `vectors_test.go`. |
| `sdk/repo/testdata/v1/config_mac.json` | Vector 9, both escrow modes. |
| `sdk/repo/testdata/v1/index.json` | Vector 5, index half: `index_1` under epoch 1 covers the vector-4 data pack; `index_2` under epoch 2 covers both packs and has `supersedes: [index_1]`. |

No new dependencies. The package uses stdlib plus `sdk/{vdcrypt,compress,chunker,pack,snapshot}`.

## Interfaces exposed (`github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo`)
```go
// backend
type Kind string // KindConfig KindKeys KindChain KindIndex KindSnapshots KindData KindLocks (= api-v1 kinds); Valid()
var Kinds []Kind; var ConfigOID = "000…0" (64)
type ObjectInfo struct{ OID string; Size int64 }
type LockKind string // LockShared "shared", LockExclusive "exclusive"
type Lock struct{ ID string; Kind LockKind; Expires time.Time }
type Backend interface {
  Put(ctx, kind Kind, oid string, data []byte) error          // content kinds: oid == sha256(data); re-put ok
  Get(ctx, kind Kind, oid string) ([]byte, error)              // ErrNotFound
  GetRange(ctx, kind Kind, oid string, offset, length int64) ([]byte, error) // ErrRange
  Stat(ctx, kind Kind, oid string) (int64, error)
  List(ctx, kind Kind) ([]ObjectInfo, error)
  Delete(ctx, kind Kind, oid string) error                     // missing = nil
  Lock(ctx, kind LockKind, ttl time.Duration) (Lock, error)    // ErrLocked
  RefreshLock(ctx, id string, ttl time.Duration) (Lock, error) // ErrNoLock
  Unlock(ctx, id string) error
}
func IsOID(s string) bool; func CheckKey(Kind, string) error; func CheckObject(Kind, string, []byte) error
type LockTable struct{ Now func() time.Time } // Lock/RefreshLock/Unlock; embed in a backend
func NewMemBackend() *MemBackend              // + Raw, SetRaw (tests)
func NewDirBackend(root string) (*DirBackend, error) // root = repo/<uuid>; Root()
var ErrNotFound, ErrInvalidObject, ErrRange, ErrLocked, ErrNoLock

// config
const FormatName="vaultdock.repo", FormatVersion=1, EscrowFull="full", EscrowMetadata="metadata", Compression="zstd", AEAD="xchacha20poly1305"
type ChunkerParams struct{ Alg string; Min, Avg, Max int64 }
type Config struct{ Format string; V int; Repo string; Created int64; EscrowMode string; Chunker ChunkerParams;
  Compression, AEAD string; PackTarget, PackMax int64; RecoveryPub []byte; MAC string }
func NewConfig(repo vdcrypt.RepoID, created int64, escrowMode string, recoveryPub vdcrypt.Key) *Config
func (c *Config) Encode(kMAC vdcrypt.Key) ([]byte, error); func (c *Config) RepoID() vdcrypt.RepoID
func ParseConfig(stored []byte) (*Config, error)
func ConfigMACInput(stored []byte) ([]byte, error); func ConfigMAC(kMAC vdcrypt.Key, stored []byte) (string, error)
func VerifyConfig(kMAC vdcrypt.Key, stored []byte) error
var ErrConfig, ErrConfigFormat, ErrConfigVersion, ErrConfigMAC

// recipients / credentials
type Recipient struct{ Kind vdcrypt.Kind; Pub vdcrypt.Key; Label string; KDF *vdcrypt.PassphraseKDF; Device *vdcrypt.KeyFileDevice; Server *vdcrypt.KeyFileServer }
func DeviceRecipient(boxPub vdcrypt.Key, deviceID int64, signPub ed25519.PublicKey, label string) Recipient
func EscrowRecipient(sekPub vdcrypt.Key, sikPub ed25519.PublicKey, sekSig []byte, sekVersion uint32, label string) Recipient
func PassphraseRecipient(rnd io.Reader, passphrase []byte, kdf *vdcrypt.PassphraseKDF, label string) (Recipient, error)
type Credential struct{…} // Kind(), Wipe()
func DeviceCredential(boxPriv vdcrypt.Key) Credential
func EscrowCredential(sekPriv vdcrypt.Key) Credential
func RecoveryCredential(text string) (Credential, error)
func RecoverySecretCredential(secret vdcrypt.Key) Credential
func PassphraseCredential(passphrase []byte) Credential

// repo
type Options struct{ Rand io.Reader; Now func() time.Time; Logger *slog.Logger }
type InitOptions struct{ Options; RepoID vdcrypt.RepoID; EscrowMode string; Recipients []Recipient }
func Init(ctx, be Backend, opts InitOptions) (*Repo, string /*VDRK1- text*/, error)
func Open(ctx, be Backend, cred Credential, opts Options) (*Repo, error) // loads the index too
const LockTTL = 5 * time.Minute
func (r *Repo) ID() vdcrypt.RepoID; Backend() Backend; Config() Config; MetadataOnly() bool
func (r *Repo) Epoch() uint32; Stale() bool; Close()
func (r *Repo) Keys() (vdcrypt.RepoKeys, error)             // ErrMetadataOnly
func (r *Repo) EpochKeys(e uint32) (vdcrypt.EpochKeys, error)
func (r *Repo) Keyring() *pack.Keyring
func (r *Repo) KeyFiles() map[string]*vdcrypt.KeyFile; RecoveryReady() bool; Recipients() []Recipient
type RotateOptions struct{ Exclude func(*vdcrypt.KeyFile) bool; Add []Recipient }
func (r *Repo) Rotate(ctx, RotateOptions) (uint32, error)
func (r *Repo) AddRecipient(ctx, Recipient) error
func (r *Repo) RemoveRecipient(ctx, match func(*vdcrypt.KeyFile) bool) (uint32, error)
func (r *Repo) SetEscrowMode(ctx, mode string) (uint32, error)
var ErrExists, ErrNoKeyFile, ErrMetadataOnly, ErrStaleEpoch, ErrNoEpoch, ErrRecipient, ErrRecoveryRequired

// index
const IndexVersion = 1, MaxIndexBlobs = 100000
type IndexBlob struct{ ID string; T pack.BlobType; O, L, R uint32 } // Entry() (pack.Entry, error)
type IndexPack struct{ Pack string; Class pack.Class; Blobs []IndexBlob }
type IndexFile struct{ V int; Supersedes []string; Packs []IndexPack }
func IndexPackOf(p *pack.Pack) IndexPack
func EncodeIndexJSON(*IndexFile) ([]byte, error); func DecodeIndexJSON([]byte) (*IndexFile, error)
func SealIndex(rnd io.Reader, repo vdcrypt.RepoID, ek vdcrypt.EpochKeys, f *IndexFile) ([]byte, error)
func OpenIndex(repo vdcrypt.RepoID, metaKeys map[uint32]vdcrypt.Key, sealed []byte) (*IndexFile, []byte, error)
type Location struct{ Pack string; Class pack.Class; Entry pack.Entry }
type Index; func NewIndex() *Index // AddPack, Lookup, Has, HasPack, Len, Files
func (r *Repo) Index() *Index; HasBlob(pack.BlobID) bool; LoadIndex(ctx) error
func (r *Repo) NewPackWriter(c pack.Class) (*pack.Writer, error)
func (r *Repo) SavePack(ctx, *pack.Pack) error; FlushIndex(ctx) ([]string, error); WriteIndex(ctx, *IndexFile) (string, error)
var ErrIndex, ErrBlobNotFound

// blobs / snapshots
func (r *Repo) ReadBlob(ctx, id pack.BlobID) ([]byte, error)
func (r *Repo) ReadTree(ctx, idHex string) (*snapshot.Tree, error)
func (r *Repo) SaveSnapshot(ctx, root *snapshot.Root, device ed25519.PrivateKey) (string, error) // flushes index first
func (r *Repo) ListSnapshots(ctx) ([]string, error)
func (r *Repo) LoadSnapshot(ctx, oid string) (*snapshot.Root, []byte, error)
func (r *Repo) DeviceSignKey(d snapshot.Device) (ed25519.PublicKey, error) // ErrNoDeviceKey
func (r *Repo) VerifySnapshot(root *snapshot.Root, stored []byte) error

// §10 lock objects (offline tooling)
type LockObject struct{ V int; Holder map[string]string; Kind LockKind; Created, Expires int64 }
func SealLock(rnd, repo, ek, *LockObject) ([]byte, error); func OpenLock(repo, metaKeys, sealed) (*LockObject, error)
func (r *Repo) SealLock(*LockObject) ([]byte, error); OpenLock([]byte) (*LockObject, error)
```

## Commands run and results
From `D:/VaultDock/VaultDock/sdk`:
- `go test ./repo/ -run TestVectors -update`: generated `config_mac.json` and `index.json` once.
- `gofmt -l repo`: clean.
- `go vet ./repo/`: ok.
- `go build ./...`: ok.
- `go test -count=1 ./repo/ ./...`: every package passes. The repo tests are:
  - `TestRoundTrip`:
    - init → data pack + meta pack → index → signed snapshot → read back;
    - reopen with each of device, recovery (lower-cased text), passphrase and escrow, then read back and write a deduplicated second backup;
    - a second `Init` gives `ErrExists`; a wrong device key or passphrase gives `ErrNoKeyFile`.
  - `TestRotation`:
    - rotate to epoch 2, then every credential reads epoch-1 data through the chain;
    - remove the device (epoch 3): the device opens stale at epoch 2, can read, and writing or rotating gives `ErrStaleEpoch`;
    - add a new device, which opens at epoch 3 and reads epoch-1 data;
    - removing recovery gives `ErrRecoveryRequired`.
  - `TestMetadataOnly`:
    - under escrow=metadata, the SEK opens metadata-only, reads trees and snapshots of epochs 1 and 2 through the meta-chain, and gets `ErrMetadataOnly` for data, keys, writes and rotation;
    - no data or chain keys are held, and the full chain does not walk with `K_meta`;
    - `SetEscrowMode(full)` gives the escrow full access, including old data.
  - `TestConfigTamper`:
    - `ErrConfigMAC` for a changed field, an added member, a changed mac or a changed recovery_pub;
    - `ErrConfigFormat` and `ErrConfigVersion` for those fields;
    - `ErrConfig` for a bad chunker, a duplicate member or broken JSON;
    - reordered but equivalent JSON still verifies (JCS).
  - `TestObjectTamper`: oid mismatch, a pack bit-flip (`pack.ErrBlobAuth`), and an unknown signer (`ErrNoDeviceKey`).
  - `TestLockObject`, `TestBackends` (mem and dir conformance), `TestDirBackendLayout` (§2 paths plus a full repo on disk), `TestLockTable`, and rotation blocked by a shared lock.
  - `TestVectors` (recompute and byte-compare) and `TestVectorFilesOpen`:
    - the frozen configs verify, and fail with a different `K_mac`;
    - the frozen indexes open, match their JSON and supersedes, and fail with the wrong key;
    - every vector-4 blob is found through the index and read back from the frozen pack bytes with `pack.ReadBlob`.
  - Because `TestVectorFilesOpen` is read-side only, it does not depend on the zstd version.
- `-race` is not available (cgo is disabled on the host).

## Contract issues
1. **Index "first match wins" has no defined order.** §7.4 does not say how indexes are ordered. Readers load index objects in **oid order**, and within an index, in pack/blob order. Duplicate locations are equally valid, so this only matters for determinism.
2. **`supersedes` is not used by readers.** Readers take the union of all indexes, as §7.4 says. A superseded index is redundant but harmless. Deleting it is GC's job (§11).
3. **Metadata-only blob reads skip the blob-id check.** Without `DK`, `K_id` is unknown, so tree/meta blobs are authenticated only by the AEAD, which has the blob id in its AD. An attacker holding `K_meta` could forge such blobs; the device signature on the root covers only the root, not the trees. This is inherent to §1.2/§5 and is not a regression.
4. **Time units are unspecified.** Config `created`, key file `created` and lock `created`/`expires` use Unix **milliseconds**, the same as the snapshot root `time` (P1-05).
5. **Config details left open:**
   - The stored bytes are compact JSON in §5 field order with the mac last and no trailing newline.
   - The mac covers **every** member except `mac`, including unknown ones (`JCS(all members − mac)`), so a member that is added later is still protected.
   - Readers accept only the v1 chunker parameters and `pack_max ≤ 64 MiB`.
   - `ParseConfig` rejects an unknown `format` and a higher `v` before any key is used (§5 step 1).
6. **Lock object JSON.** `holder` is unspecified (`{…}`); it is implemented as a string→string map. The payload is `0x01 || JSON` (uncompressed), as §10 shows.
7. **Mixed-epoch stale state is undefined.** The contract does not say what a recipient should do when it holds an older epoch than the newest chain object. Here such a `Repo` is "stale": it reads every epoch it can reach and refuses all writes (`ErrStaleEpoch`). The current epoch is the highest envelope epoch among `chain/` objects.

## Gaps / follow-ups
- `pack` has no metadata-only open: `pack.OpenBlob` always recomputes the id with `K_id`. `repo` therefore has a private `openMetaBlob` that copies `OpenBlob`'s logic without the id check. INT may want `pack` to export one (e.g. a `Keyring` flag), which would remove the duplicate.
- Escrow key files are not verified against a pinned SIK during open or rotation. Callers (the device engine) should call `kf.VerifyEscrow(pinnedSIK)` on `Recipients()`/`KeyFiles()` before trusting the escrow recipient. An `Options.PinnedSIK` could do this automatically.
- No GC, compaction or forget: `WriteIndex` with `Supersedes`, plus `Backend.Delete`, are the building blocks.
- Passphrase NFC normalisation is still the caller's job (vdcrypt issue 4).
- `DirBackend` locks are process-local. `vaultdock-restore` on a shared copy should also write `LockObject`s.
- Rotation and `SetEscrowMode` log at Info (repo id, epoch, recipient count; no secrets), so tests print a few log lines.

## Notes for dependent tasks / INT
- **P1-10 (HTTP backend):** implement `Backend` over `client.{Put,Get,Head,List}Object` and the lock API.
  - `Kind` values equal the api-v1 kinds, and config uses `ConfigOID`.
  - `Get`/`Stat` must map 404 to `ErrNotFound`, `GetRange` maps to an HTTP Range, and a 409 `repo_locked` maps to `ErrLocked` (wrapped).
  - `Lock` returns `Lock{ID: lock_id, Expires: time.UnixMilli(expires_at)}`. Check whether `expires_at` is in ms.
  - `List` must page through every cursor.
  - The config PUT needs an exclusive lock: `Init`/`SetEscrowMode` hold one while they write config.
- **Engine (P1-22/P1-25):** the writer flow is:
  1. `r.NewPackWriter(pack.ClassData|ClassMeta)`;
  2. dedup with `r.HasBlob(id) || w.Has(id)`;
  3. `w.Finish()`, then `r.SavePack`;
  4. `r.SaveSnapshot(root, deviceKey)`, which flushes the pending index first (§11 order).

  `Keys()` gives `K_chunker` (`chunker.NewGear`), `K_fh` and `K_id`.
- **Server (P1-12/P1-16):**
  - Open with `EscrowCredential(sekPriv)`. Under metadata escrow, `MetadataOnly()` is true: trees and snapshots are readable, data is not, and the config mac is not verified (§5).
  - Use `AddRecipient(DeviceRecipient(...))` for a new device (full escrow), and `Rotate`/`RemoveRecipient` for revocation.
  - `RecoveryReady()` backs the `recovery_key_confirmed` readiness check.
- Regenerate vectors only on a format change: `go test ./repo/ -run TestVectors -update`.
