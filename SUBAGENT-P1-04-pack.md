# P1-04 SDK-REPO: pack (data/meta classes) + test vectors 3–4
Status: completed

## Goal
Package `sdk/pack` implementing backup-format-v1 (rev 1.2) §7.1–§7.3: blob ids under `K_id`, blob encoding (via `sdk/compress`) and sealing under `K_data_e`/`K_meta_e` by class, the pack writer (shuffle, header, sealed header, trailer, oid, `pack_target`/`pack_max`), and a ranged-read-friendly reader with strict validation. Also freeze test vectors 3 and 4 (§13).

## Changes (repo: VaultDock, all created)
- `sdk/pack/pack.go`: classes and types, format constants, typed errors, `BlobID`/`ComputeID`, `Entry`, `Keyring`, and `SealBlob`/`OpenBlob`. `OpenBlob` decrypts, decodes within `raw_len` and the per-type limit, then checks the id with constant-time `hmac.Equal`.
- `sdk/pack/writer.go`: `Writer` (Add/Finish/Full/Size/Len/Has). It enforces the class, raw limits (data 8 MiB, tree/meta 32 MiB), duplicate ids and `pack_max`, and runs an unbiased Fisher–Yates shuffle from the RNG.
- `sdk/pack/reader.go`: `ParseTrailer`, `ParseHeader`, `NewReader` (two `ReadAt` calls: trailer, then nonce plus header), `Reader.Blob`/`Find`, and `ReadBlob` (index-only path: one `ReadAt` of `sealed_len` bytes).
- `sdk/pack/pack_test.go`: round trip (checks the read count), shuffle, and negative tests: truncation at every head and tail cut plus short sources, wrong trailer (magic, NUL, version, length variants), header tamper (nonce, envelope version and epoch, ciphertext, swapped header), blob tamper (every region plus bad entry fields), blob id mismatch, wrong K_id, bad comp flag, wrong class key (as meta, swapped key, metadata-only keyring → `ErrNoKey`, nil keyring, wrong repo), oversized blobs (writer and reader), `pack_target`/`pack_max`, bad options, and a `ParseHeader` table. It also seals crafted headers with the real key to test the checks after decryption.
- `sdk/pack/vectors_test.go`: generates and compares vectors 3 and 4 (byte-exact, `-update` to regenerate) and opens the frozen files independently of the generators.
- `sdk/pack/fuzz_test.go`: `FuzzReader` (whole-pack reader, both classes) and `FuzzParseHeader` (the plaintext header checks that the AEAD otherwise hides from the fuzzer).
- `sdk/repo/testdata/v1/pack_blobs.json`: vector 3. Five blobs: data-zstd, data-incompressible and data-precompressed under epoch 1 with `K_data`; tree and meta under epoch 2 with `K_meta`. Each records raw, blob_id, AD, comp, payload and sealed.
- `sdk/repo/testdata/v1/pack_packs.json`: vector 4. A data pack (epoch 1, 3 blobs, oid `d4842620…584f`, 1464 B) and a meta pack (epoch 2, tree plus meta, oid `0efb1fa9…921e`, 465 B). Each records inputs, entries, pack_nonce, header_plain, sealed_header, trailer, size, oid and the full pack as hex.

There are no binary fixtures (§13 does not ask for them) and no new dependencies.

## Interfaces exposed (package `github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack`)
```go
type Class uint8;    const ClassData, ClassMeta Class = 0, 1
type BlobType uint8; const TypeData, TypeTree, TypeMeta BlobType = 0, 1, 2
func (t BlobType) Class() (Class, error); func (t BlobType) MaxRaw() int
const NonceSize=16, TrailerSize=12, Version=0x01, IDSize=32, EntrySize=45, TargetSize=16<<20, MaxSize=64<<20
type BlobID [32]byte // String() hex
func ComputeID(kID vdcrypt.Key, raw []byte) BlobID
type Entry struct{ Type BlobType; ID BlobID; Offset, Length, RawLength uint32 } // index t,id,o,l,r
type KeyFunc func(epoch uint32) (vdcrypt.EpochKeys, error)
type Keyring struct{ Repo vdcrypt.RepoID; ID vdcrypt.Key; Epochs KeyFunc }
func SealBlob(rnd io.Reader, repo vdcrypt.RepoID, kID vdcrypt.Key, ek vdcrypt.EpochKeys, t BlobType, raw []byte, precompressed bool) (Entry, []byte, error)
func OpenBlob(kr *Keyring, e Entry, sealed []byte) ([]byte, error)

type WriterOptions struct{ Rand io.Reader; Target, Max int } // 0 = 16 MiB / 64 MiB; Max ≤ MaxSize
type Pack struct{ OID string; Bytes []byte; Class Class; Epoch uint32; Entries []Entry } // entries in offset order
func NewWriter(repo vdcrypt.RepoID, kID vdcrypt.Key, ek vdcrypt.EpochKeys, c Class, opts WriterOptions) (*Writer, error)
func (w *Writer) Add(t BlobType, raw []byte, precompressed bool) (Entry, error) // Offset filled at Finish
func (w *Writer) Has(id BlobID) bool; Len() int; Size() int; Full() bool
func (w *Writer) Finish() (*Pack, error)

func ParseTrailer(trailer []byte, size int64) (uint32, error)
func ParseHeader(plain []byte, c Class, dataEnd int64) ([]Entry, error)
type Reader struct{ Class Class; Epoch uint32; Nonce [16]byte; Entries []Entry; Size, DataEnd int64 }
func NewReader(r io.ReaderAt, size int64, kr *Keyring, c Class) (*Reader, error)
func (pr *Reader) Find(id BlobID) (Entry, bool)
func (pr *Reader) Blob(e Entry) ([]byte, error)
func ReadBlob(r io.ReaderAt, kr *Keyring, e Entry) ([]byte, error)
```
Errors (each failure wraps exactly one, so check with `errors.Is`):
- `ErrTruncated`, `ErrTrailer`, `ErrVersion`, `ErrHeaderLength`
- `ErrHeaderAuth`, `ErrHeader`, `ErrClass`, `ErrInvalidType`
- `ErrBlobAuth`, `ErrBlobPayload`, `ErrBlobID`, `ErrBlobTooLarge`, `ErrBlobBounds`
- `ErrPackFull`, `ErrPackTooLarge`, `ErrEmpty`, `ErrDuplicate`, `ErrFinished`, `ErrNoKey`, `ErrOptions`

The auth errors also wrap the underlying `vdcrypt` error, and payload errors wrap the `compress` error.

## Commands run and results
All from `D:/VaultDock/VaultDock/sdk`:
- `gofmt -l pack`: clean.
- `go vet ./pack/`: ok.
- `go build ./...`: ok.
- `go test ./pack/ -run TestVectors -update`: generated `pack_blobs.json` and `pack_packs.json` once.
- `go test -count=1 -v ./pack/`: 16/16 PASS, including vector recompute and the independent open.
- `go test ./pack/ -run=^$ -fuzz=^FuzzReader$ -fuzztime=15s`: PASS, about 6.1M execs.
- `go test ./pack/ -run=^$ -fuzz=^FuzzParseHeader$ -fuzztime=15s`: PASS, about 10.6M execs.

## Contract issues
1. **Header entry order is unspecified.** The writer lists entries in physical (offset) order after the shuffle. The reader does not require offset order. It does require the blobs to tile `[0, start of pack_nonce)` exactly with no gaps or overlaps, which is implied by the §7.3 layout. `offset` is from the pack start (rev 1.0 text).
2. **Empty packs.** The contract does not forbid `count = 0`. The writer never produces one, and the reader rejects it (`ErrEmpty`).
3. **Reader limits beyond the literal text.**
   - A pack over 64 MiB (`pack_max` / `max_object_bytes`) is rejected.
   - `sealed_len` must be between 46 and `MaxRaw(type) + 46`. This follows from §7.2: raw is stored when compression does not pay.
   - Duplicate blob ids within one pack are rejected. Across packs they are allowed, per §7.4.
   - If a future config ever allows `pack_max` above 64 MiB, `MaxSize` must change.
4. **The RNG consumption order is a vector convention.** It is: one nonce per `Add`, then the shuffle (u64 draws with rejection sampling), the 16-byte `pack_nonce`, and the header envelope nonce. Other implementations need it only to reproduce vector 4, not to read packs.
5. **Vector stability depends on the zstd encoder.** The zstd blobs in vectors 3 and 4 (comp = 1) are byte-exact for `klauspost/compress v1.20.3`. A future library upgrade could change the compressed bytes and fail `TestVectors` even though the format is unchanged. `TestVectorFilesOpen` (the read side) stays valid regardless. INT may want to decide whether the recompute check should exclude comp = 1 bodies after upgrades.
6. **A blob's epoch is taken from its own envelope**, not the header's. This allows future repacking across epochs; the writer always uses one epoch per pack.

## Gaps / follow-ups
- The 30 s idle sealing trigger and "seal at job end" are caller (repo/engine) policy. The writer offers `Full()` (≥ `pack_target`) and `ErrPackFull` (would exceed `pack_max`; the blob is not added).
- The writer holds sealed blobs in memory until `Finish` (≤ 64 MiB per pack); there is no streaming writer.
- No index code here (P1-05 or the index task): `Entry` maps 1:1 onto the index fields `t,id,o,l,r`.

## Notes for dependent tasks / INT
- Index writer: use `Pack.OID`, `Pack.Class` and `Pack.Entries`.
- Restore and the server can call `ReadBlob(rangeReader, kr, entry)` directly from index entries, with no header fetch. Use `NewReader` for verification or compaction (it validates the whole header).
- Metadata-escrow holders: have `KeyFunc` return `EpochKeys` with a zero `Data` key. Data packs then fail cleanly with `ErrNoKey`.
- `Writer.Add` returns `ErrDuplicate` for an id already in the pack. Cross-pack dedup belongs to the caller (`Has` helps within a pack).
- Regenerate the vectors (a format change only): `go test ./pack/ -run TestVectors -update`.
- INT's `go mod tidy` is still pending for `klauspost/compress` and `x/crypto` (both direct now).
