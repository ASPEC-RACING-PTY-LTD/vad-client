# P1-02 SDK-REPO: compress
Status: completed

## Goal
Package `sdk/compress` implementing the blob payload encoding of backup-format-v1 §7.2 (`payload = comp_u8 || body`, comp 0 = raw, 1 = zstd level 3, raw when compressed ≥ 0.97 × raw or known-compressed), plus the `0x01 || zstd(JSON)` framing used by index (§7.4) and snapshot (§8.1) objects, with bounded decompression.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `sdk/compress/compress.go` (created): encoder/decoder, pooled zstd instances, limits.
- VaultDock: `sdk/compress/compress_test.go` (created): table tests (round trip, threshold, incompressible rollback, limit/zip-bomb incl. frames without content size, corrupt/truncated/bit-flipped input, unknown flag, concurrency) and two 1 MiB benchmarks.
- `go.mod`/`go.sum` not edited by me (go.mod already listed `klauspost/compress v1.20.3 // indirect`; it is now a direct import, so INT's tidy should drop `// indirect`).

## Interfaces exposed
```go
const CompRaw byte = 0; const CompZstd byte = 1
const Level = 3
const MaxDataRaw = 8 << 20  // type 0
const MaxMetaRaw = 32 << 20 // types 1, 2
var ErrEmpty, ErrUnknownComp, ErrTooLarge, ErrSizeMismatch, ErrCorrupt error

func Encode(raw []byte, precompressed bool) []byte
func AppendEncode(dst, raw []byte, precompressed bool) []byte
func EncodeZstd(raw []byte) []byte                         // always 0x01||zstd (index/snapshot)
func Decode(payload []byte, rawLen int) ([]byte, error)     // exact raw_len, rawLen ≤ MaxMetaRaw
func DecodeLimit(payload []byte, max int) ([]byte, error)   // unknown length, clamped to MaxMetaRaw
```
Behaviour:
- Threshold: zstd kept only if `len(body)*100 < len(raw)*97`; empty input is stored raw.
- Decode never produces more than `rawLen` (or `max`) bytes: it rejects a declared frame content size over the limit up front, then streams through a pooled decoder reading at most limit+1 bytes. Decoders are also capped at 32 MiB memory/window.
- Encoders (`WithEncoderConcurrency(1)`, level `EncoderLevelFromZstd(3)`, default frame CRC) and decoders (`WithDecoderConcurrency(1)`) live in `sync.Pool`s. All functions are safe for concurrent use. Decoded output is always a fresh slice (raw bodies are cloned).
- No dictionaries (the contract does not define any).

## Commands run and results
- `gofmt -l compress`: no output
- `go vet ./compress/`: ok
- `go test -v ./compress/`: all PASS
- `go test -race`: not available (cgo disabled on host)
- `go test -bench . -benchtime 20x ./compress/` (i9-14900K, 1 MiB half-random/half-text): Encode about 2.8 GB/s, Decode about 2.1 GB/s

## Contract issues
- §7.2 says "≥ 0.97 × raw" without saying whether "compressed form" means the body or the whole payload. I compare the zstd body length (excluding the comp byte) with the raw length. Either choice decodes the same way; it only affects writer choice.
- "Known-compressed types" is not enumerated in the contract. Callers decide this (the `precompressed` flag); `sdk/mimetypes` could supply it.
- Use of the zstd frame checksum is unspecified. I left the default (CRC on, 4 bytes per frame); the AEAD already authenticates the data.

## Gaps / follow-ups
- Per-type limit: `Decode` caps at `MaxMetaRaw`. `pack`/`repo` must also enforce `raw_len ≤ MaxDataRaw` for type 0 (and validate it against header values before calling).

## Notes for dependent tasks / INT
- Pack writer: `payload := compress.Encode(chunk, isKnownCompressed)`, then seal. Reader: `compress.Decode(opened, int(rawLen))` with `raw_len` from the header/index.
- Index/snapshot: `compress.EncodeZstd(json)` / `compress.DecodeLimit(opened, compress.MaxMetaRaw)`. `DecodeLimit` also accepts comp=0, so callers that require `0x01` must check `opened[0] == compress.CompZstd` themselves.
- INT: `go mod tidy` should mark `github.com/klauspost/compress` as a direct dependency. Licence: BSD-3-Clause (checked in the module cache LICENSE).
