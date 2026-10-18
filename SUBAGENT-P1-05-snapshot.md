# P1-05 snapshot (keyed file hash, JCS, signature) + vector 5
Status: completed

## Goal
Package `sdk/snapshot` per backup-format-v1 rev 1.2 §3, §6, §8, §12 and §13. It provides:
- the snapshot root, tree and meta-blob JSON types;
- §6 path tokens;
- RFC 8785 JCS;
- the keyed whole-file hash `h = HMAC-SHA256(K_fh, file)`;
- the Ed25519 root signature over `JCS(root without sig)`;
- the sealed root `seal(K_meta_e, 0x01 || zstd(JSON), AD="vd1:snapshot:<uuid>")`;
- tree-blob encode/decode for `sdk/pack`;
- the snapshot half of vector 5.

## Changes (repo VaultDock, all created)
- `sdk/snapshot/jcs.go`: `Canonicalize` and `MarshalJCS`, written from the RFC. Keys are sorted by UTF-16 code units, numbers use the ECMAScript Number→String algorithm, and only the RFC escapes are used. It rejects duplicate keys, invalid UTF-8, nesting deeper than 64, and integer literals that a double cannot hold exactly.
- `sdk/snapshot/filehash.go`: the K_fh hash (streaming, bytes, constant-time verify), plus `BlobID` (HMAC-SHA256(K_id, raw), §7.2) for naming tree and meta blobs.
- `sdk/snapshot/tokens.go`: §6 tokens.
  - `Tokens.Encode` picks the longest match, compares case-insensitively at component boundaries, accepts `\\?\` and `\\?\UNC\` input, and collapses doubled separators.
  - Also provides `Resolve` (remap), `Split`/`JoinTokenPath`, `VolumeToken`, `ValidToken` and `ExtendedPath`.
  - Components may not be empty, `.`, `..`, or contain `/ \ : NUL` or invalid UTF-8.
- `sdk/snapshot/root.go`: the `Root` type and sub-types, plus `Validate`, `SigningBytes`, `Sign`, `Encode`, `DecodeRoot`, `VerifyRoot`, `SealRoot` and `OpenRoot`.
- `sdk/snapshot/tree.go`: the `Tree`/`Entry`/`Ext`/`ADS` types.
  - `EncodeTrees` sorts entries bytewise by `n` (tie-break `nb`), rejects duplicates, and splits at 50 000 entries with a backward-built `cont` chain.
  - `DecodeTree` validates the result.
- `sdk/snapshot/meta.go`: `inventory/1` and `registry/1` meta-blob types, with `EncodeMeta`, `MetaSchema`, `DecodeInventory` and `DecodeRegistry`.
- Tests: `jcs_test.go`, `tokens_test.go`, `root_test.go`, `tree_test.go` and `vectors_test.go`.
- `sdk/repo/testdata/v1/snapshot_root.json`: vector 5 (snapshot part).

No new dependencies. The package uses stdlib, `sdk/vdcrypt` and `sdk/compress`.

## Interfaces exposed (`github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot`)
```go
// JCS
func Canonicalize(data []byte) ([]byte, error)
func MarshalJCS(v any) ([]byte, error)
func FormatJCSFloat(f float64) string
var ErrJCSInvalid, ErrJCSDuplicateKey, ErrJCSNumber error

// hashes
func NewFileHash(kfh vdcrypt.Key) hash.Hash            // pass RepoKeys.FileHash
func FileHash(kfh vdcrypt.Key, r io.Reader) ([32]byte, error)
func FileHashBytes(kfh vdcrypt.Key, data []byte) [32]byte
func VerifyFileHash(sum [32]byte, entryHex string) bool // constant time
func BlobID(kid vdcrypt.Key, raw []byte) [32]byte      // pass RepoKeys.ID

// path tokens
const TokenProfile … TokenProgramFilesX86 = "{PROFILE}" … "{PROGRAMFILESX86}"
type Tokens map[string]string
func (t Tokens) Encode(winPath string) (string, error)
func (t Tokens) Resolve(tokenPath string) (string, error)
func SplitTokenPath(tp string) (token string, components []string, err error)
func JoinTokenPath(token string, components ...string) (string, error)
func VolumeToken(guid string) (string, error)  // "{VOL:<lowercase guid>}"
func ValidToken(tok string) bool
func ExtendedPath(p string) string
var ErrToken, ErrTokenPath, ErrNoTokenMatch, ErrTokenUnmapped error

// root
const Version = 1; InstallModeSimple = "simple"; InstallModeAdvanced = "advanced"
type Root struct{ V int; Repo string; Time int64; Parent *string; Job Job; Device Device; Host Host;
  Tokens Tokens; Roots []RootRef; Inventory *string; Stats Stats; Tags []string;
  IssuesSummary IssuesSummary; Epoch uint32; Profile Profile; InstallMode string; Sig string }
// json: v repo time parent job{id,name,kind,mode} device{id,name,key_id}
//   host{hostname,os,user,sid,machine_guid_hash} tokens roots[{token_path,tree}] inventory
//   stats{files,dirs,bytes} tags issues_summary{skipped,by_class} epoch
//   profile{sid,username,consent_id} install_mode sig
func (r *Root) Validate() error
func (r *Root) SigningBytes() ([]byte, error)          // JCS(root without sig)
func (r *Root) Sign(device ed25519.PrivateKey) error   // sets Sig (std b64)
func (r *Root) Encode() ([]byte, error)                // stored JSON = JCS incl. sig
func DecodeRoot(data []byte) (*Root, error)
func VerifyRoot(data []byte, devicePub ed25519.PublicKey) error  // on the stored bytes
func SealRoot(rnd io.Reader, repo vdcrypt.RepoID, meta vdcrypt.EpochKeys, r *Root) ([]byte, error)
func OpenRoot(repo vdcrypt.RepoID, metaKeys map[uint32]vdcrypt.Key, sealed []byte) (*Root, []byte, error)
var ErrRoot, ErrNoSignature, ErrSignature, ErrPayload, ErrRootEpoch, ErrNoEpochKey error

// trees
const MaxTreeEntries = 50000; TypeFile="f"; TypeDir="d"; TypeSymlink="l"; TypeReparse="r"
type Tree struct{ V int; Entries []Entry; Cont *string }
type Entry struct{ N string; NB *string; T string; S, M, C int64; A uint32; H *string;
  Ch []string; Sub, Ln *string; X *Ext }        // json n nb t s m c a h ch sub ln x
type Ext struct{ ADS []ADS; Placeholder bool }   // x.ads, x.placeholder (omitempty)
type ADS struct{ Name string; Ch []string; S int64 }
type TreeBlob struct{ ID [32]byte; Raw []byte }  // IDHex()
func EncodeTrees(kid vdcrypt.Key, entries []Entry) ([]TreeBlob, error) // [0] = head
func DecodeTree(raw []byte) (*Tree, error)
func HexPtr(s string) *string
var ErrTree error

// meta blobs
const SchemaInventory = "inventory/1"; SchemaRegistry = "registry/1"
type Inventory, App, Game, DevProject, NotPortable, Registry, RegistryValue
func EncodeMeta(v any) ([]byte, error); func MetaSchema(raw []byte) (string, error)
func DecodeInventory(raw []byte) (*Inventory, error); func DecodeRegistry(raw []byte) (*Registry, error)
var ErrMeta error
```

## Commands run and results
From `D:/VaultDock/VaultDock/sdk`:
- `gofmt -l snapshot`: clean.
- `go vet ./snapshot/`: ok.
- `go test ./snapshot/ -run TestVector5 -update`: generated the vector once.
- `go test -count=1 ./snapshot/`: all 20 tests pass, including `TestVector5` (recompute and byte-compare) and `TestVector5FileOpens` (opens the frozen sealed root with K_meta_2, verifies the signature, recomputes tree ids and file hashes, and fails with K_meta_1).
- `go build ./...`: ok.

Tests cover:
- the RFC 8785 sample and UTF-16 sort sample, escapes, -0, exponent boundaries, extreme doubles, duplicate keys, inexact integers, invalid UTF-8, trailing data and depth;
- signature tamper: changed, added and duplicated members, every signature character flipped, wrong key, short key, missing sig, bad base64;
- unknown future fields staying covered by the signature;
- the wrong epoch key, a data key used instead of the meta key, a missing epoch, another repo's AD, a tampered envelope epoch field, and a root epoch that differs from the envelope epoch;
- path tokens: longest match, component boundary, case-insensitive prefix, Unicode/emoji, Windows names (`CON`, trailing dot or space), `\\?\` input, a long path of about 9 KB, remapping, and rejection of traversal, ADS and NUL;
- tree sort, split, `cont` chain, duplicates, and the `[]` for an empty file.

## Contract issues
1. **Root field list lives only in rev 1.0.** §8.1 says "same as revision 1.0". The rev 1.0 fields were taken from `contracts/history/backup-format-v1-rev1.0.md` §8.1.
2. **Root `time` unit is unspecified.** Implemented as Unix **milliseconds** (the api-v1 timestamp convention). Unix nanoseconds would exceed 2^53 and could not be JCS-signed exactly.
3. **JCS and large integers.** RFC 8785 treats numbers as doubles. Signed JSON must therefore keep integers exactly representable (≤ 2^53 in practice). `Canonicalize` rejects inexact integer literals rather than rounding them, so a signature can never be ambiguous. Trees are not JCS'd, so their nanosecond `m`/`c` values are unaffected.
4. **Stored root bytes are unspecified.** I store `JCS(root including sig)`. Verification always runs on the stored bytes (sig stripped, then JCS), so unknown future fields stay signed.
5. **The root epoch must match the envelope epoch, and the repo must match.** This is not stated but is enforced on seal and open, as a binding check.
6. **`{VOL:<guid>}` GUID format is unspecified.** Writers emit a lowercase 8-4-4-4-12 GUID without braces, and readers require that form.
7. **Encoding choices left open:**
   - Path token components forbid `/ \ : NUL`, `.` and `..`.
   - Tree names forbid the same characters.
   - The `nb` tie-break order is used for names that are equal in UTF-8.
   - `h` is required for `t=f`; `ch` is `[]` for an empty file.
   - `registry/1` `subkeys` ("same shape") use `{key, values, subkeys}`, with `schema` and `hive` only at the top level.
   - Tree and meta raw JSON is compact `encoding/json` without HTML escaping, in struct field order (deterministic, so dedup works).
8. **Vector 5 in §13 also requires "an index".** The index is owned by `sdk/repo` (P1-07), so this vector file covers only the snapshot root, trees and file hashes.
9. **zstd bytes in vectors depend on the klauspost/compress version.** This also affects P1-04's comp=1 vectors. A library upgrade that changes level-3 output would break the byte-compare even though readers stay compatible. INT may want vectors to compare the opened payload rather than the sealed bytes.

## Gaps / follow-ups
- Only the snapshot half of vector 5 is done; the index half belongs to P1-07.
- `BlobID` duplicates the §7.2 formula that `sdk/pack` will also implement; INT can consolidate.
- NTFS case folding is approximated with `strings.EqualFold`; NTFS uses its own upcase table.

## Notes for dependent tasks / INT
- Writer flow:
  1. `EncodeTrees(keys.ID, entries)`, then hand each `TreeBlob.Raw` to the meta pack writer as type 1 under id `TreeBlob.ID`.
  2. Build `Root` with `Roots[].Tree = blobs[0].IDHex()`.
  3. `root.Sign(deviceEd25519)`.
  4. `SealRoot(nil, repo, epochKeys, root)`.
  5. Store the result at `snapshots/<vdcrypt.OID(sealed)>`.
- Reader flow: `OpenRoot(repo, vdcrypt.ResolveMetaKeys(...), sealed)` returns `(root, json)`. Then call `VerifyRoot(json, signPubFromDeviceKeyFile(root.Device.KeyID))`. A bad signature is a hard error; a missing key file is a warning (§8.1), and that policy belongs to the caller.
- Regenerate the vector only on a format change: `go test ./snapshot/ -run TestVector5 -update`.
