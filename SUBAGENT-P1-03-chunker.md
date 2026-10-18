# P1-03 chunker `vd-fastcdc-1` + test vector 2 (SDK-REPO)
Status: completed

## Goal
Implement `sdk/chunker` per backup-format-v1 rev 1.1 §9: normalised FastCDC with a gear table keyed by `K_chunker` (§3.2), min 512 KiB / avg 1 MiB / max 8 MiB, masks `0xFFFFFC0000000000` / `0xFFFFC00000000000`. Provide a streaming chunker, a non-streaming helper, determinism tests, a benchmark, and freeze vector 2 (§13).

## Changes (repo: VaultDock, all created)
- `sdk/chunker/chunker.go`: constants, `Gear`/`NewGear`, `Gear.Cut`, `Gear.Boundaries`, `Gear.Split`, and the streaming `Chunker` (16 MiB reusable window, zero-copy chunk data, `Reset` for reuse).
- `sdk/chunker/chunker_test.go`: determinism across `bytes.Reader`, `iotest.OneByteReader`, `iotest.HalfReader`, `iotest.DataErrReader`, OneByte+Half, and two seeded random-size readers (1 B to 3 MiB per read), all through one reused `Chunker`. Also tests size edges (empty, 1 B, below min, exactly min, all-zero hard cuts at max, max, max+1), bounds and average on 64 MiB, `Split` against `Boundaries`, keyed (a different key gives different cuts), locality after a one-byte edit, read errors (sticky error, timeout, and `ErrNoProgress` for a reader that returns `(0, nil)` forever), and the benchmarks `BenchmarkCut` and `BenchmarkChunker`.
- `sdk/chunker/vectors_test.go`: generates vector 2 and byte-compares it with the frozen file (`-update` rewrites it). `TestVectorFileStreaming` re-checks the frozen boundaries through the streaming chunker with a random-size reader, and checks that `k_chunker` appears in vector 1 (`crypto_subkeys.json`).
- `sdk/repo/testdata/v1/chunker_boundaries.json` (vector 2): params, inputs, `k_chunker`, a gear excerpt (indices 0–3, 127, 128, 254, 255), a SHA-256 of the full gear table, the stream SHA-256, and 27 chunks (offset, length, sha256).

No new dependencies: stdlib + `vdcrypt` (+ `vdcrypt/vdcrypttest` in tests only).

## Interfaces exposed (package `github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/chunker`)
```go
const Name = "vd-fastcdc-1"
const MinSize = 512 << 10; AvgSize = 1 << 20; MaxSize = 8 << 20
const MaskS uint64 = 0xFFFFFC0000000000; MaskL uint64 = 0xFFFFC00000000000
type Gear [256]uint64
func NewGear(kChunker vdcrypt.Key) *Gear         // pass vdcrypt.DeriveRepoKeys(repo, dk).Chunker
func (g *Gear) Cut(data []byte) int              // first chunk length; data must be >= MaxSize or the whole remainder
func (g *Gear) Boundaries(data []byte) []int     // chunk lengths of a whole file
func (g *Gear) Split(data []byte) [][]byte       // sub-slices, no copy
type Chunk struct{ Offset uint64; Length int; Data []byte } // Data valid until next Next/Reset
var ErrNoProgress error
type Chunker struct{ ... }                        // not concurrency-safe
func New(r io.Reader, g *Gear) *Chunker
func (c *Chunker) Reset(r io.Reader)              // new file, keeps the 16 MiB buffer
func (c *Chunker) Next() (Chunk, error)           // io.EOF after the last chunk; read errors are sticky
```
A `*Gear` is immutable after `NewGear` and can be shared across goroutines; use one `Chunker` per goroutine.

## Commands run and results
From `D:/VaultDock/VaultDock/sdk`:
- `go test ./chunker/ -run TestVectors -update`: generated `chunker_boundaries.json` once.
- `gofmt -l chunker`: clean. `go vet ./chunker/`: ok.
- `go test -count=1 -v ./chunker/`: all 8 tests (and subtests) pass in about 1 s.
- `go test -bench . -run ^$ ./chunker/` on an i9-14900K: `BenchmarkCut` **4450 MB/s** (in memory), `BenchmarkChunker` **3035 MB/s** (streaming from a `bytes.Reader`, including the buffer copy and compaction).

## Contract issues
None blocking. These points are underspecified, and my interpretations are recorded in the vector file:
1. **Vector-2 stream.** "32 MiB SHA-256-counter stream (seed `"vaultdock"`)" is implemented as the concatenation of `SHA-256("vaultdock" || u64le(i))` for i = 0, 1, …, truncated to 33554432 bytes. There is no separator and no `vdcrypttest.NewRNG` domain prefix. The stream's SHA-256 is in the vector.
2. **Cut semantics** (standard FastCDC):
   - if `remaining <= min`, the whole remainder is one chunk;
   - otherwise `n = min(remaining, max)` and `normal = min(avg, n)`;
   - start with `h = 0` and hash bytes `i = min … normal-1` with `MaskS`, then `normal … n-1` with `MaskL`;
   - on a match at byte `i`, the chunk length is `i+1`;
   - otherwise the chunk length is `n`.
   The gear message is `"gear" || u8(i)` (5 bytes); the HMAC key is the raw 32-byte `K_chunker`.
3. Because hashing starts after `min`, the effective mean chunk size is about 1.2 MiB, not 1 MiB (27 chunks over 32 MiB in vector 2). This is inherent to §9 as written. It is informational only.

## Gaps / follow-ups
- No `io.WriterTo`/channel-style API and no parallel chunking; the packer (P1-04/05) can wrap `Next`.
- The 16 MiB window is allocated lazily on the first `Next`; callers that chunk many files concurrently should pool `Chunker`s.

## Notes for dependent tasks / INT
- Config `chunker` field value: `chunker.Name` (`"vd-fastcdc-1"`).
- Chunk `Data` aliases the internal buffer. Hash, encrypt or copy it before the next `Next()`/`Reset()`.
- To regenerate vector 2 (a format change only, never in v1): `go test ./chunker/ -run TestVectors -update`.
