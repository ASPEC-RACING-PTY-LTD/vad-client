# P0-06 Upload hardening (B7, B8)
Status: completed

## Goal
Harden the upload path against research 01 findings R3 (B7: corrupted chunk retry overwrites good data), R4 (B8: chunk write racing with complete/cancel) and the store side of R5 (maintenance reaping uploads mid-finalise). Existing API routes and response shapes are kept; device body hashing (`X-VD-Content-SHA256` -> `p.DeclaredBodySHA`) is unchanged and still authoritative for devices.

## Changes (repo: path, created/modified/deleted, why)
- vad-server: `internal/api/uploads.go` (modified)
  - **R3:** chunk receive split into `receiveChunk` (stream + hash + length/hash check). A retry of an *already received* chunk is staged to `<tmp>.<idx>.stage`, verified, and only then copied into the `.part` file at the offset (`receiveChunkStaged`). A corrupt retry returns 422 `chunk_hash_mismatch` (or 400 `bad_length`) and the stored chunk stays intact. First writes of a not-yet-received chunk still go straight into the part file, because the region is unmarked and a failed write is harmless. The staging file name doesn't match an upload id, so `CleanOrphans` removes it after a crash.
  - **R4:** new package-level `uploadLocks` (`keyedLocks`: ref-counted `RWMutex` per key, context-aware acquire with a `TryLock` fast path).
    - Chunk writers hold the upload key **shared**, plus an exclusive per-chunk key (`<id>#<idx>`) that serialises duplicate retries of one index. Under the locks they re-read the upload, so status and received bitmap are fresh: a non-`open` status gives 409 `upload_closed`.
    - `complete` takes the upload key **exclusively** around the re-read, the `Complete()` check and the `open -> finalizing` transition. No chunk can therefore be mid-write while the file is hashed or renamed into the blob store, and later chunks see `finalizing`.
    - `MarkChunk` `ErrConflict` now maps to 409 `upload_closed` (it was a 500).
  - **R5 (API side):** `cancelUpload` now returns `bool`. It takes the exclusive lock and uses `DeleteUploadUnlessFinalizing`. It removes the temp file and finishes the transfer only when the row was actually deleted, and calls `FinishTransfer("cancelled")` only for open or finalizing sessions, so a done upload's `completed` transfer is no longer overwritten. `DELETE /uploads/{id}` on a finalising upload returns 409 `upload_busy`; otherwise it returns 200 `{"ok":true}` as before (idempotent when the row is already gone).
- vad-server: `internal/store/uploads.go` (modified)
  - `MarkChunk`: bounds check of `idx` against the bitmap (it returns `ErrConflict` instead of panicking on an out-of-range index).
  - New `DeleteUploadUnlessFinalizing`.
  - `ExpiredUploads` never returns `finalizing` rows (R5). `RecoverAfterRestart` already reopens finalisations interrupted by a crash, after which normal expiry applies.
  - gofmt also normalised this file from CRLF to LF.
- vad-server: `internal/api/uploads_hardening_test.go` (created): corrupt, short and identical retries of a received chunk, with downloaded content verified; chunk/cancel while finalizing (409 / `upload_busy`); maintenance doesn't reap an expired finalizing upload; chunk after complete is 409; out-of-range index is 400; cancel of an open upload deletes it.
- vad-server: `internal/store/uploads_test.go` (created): `MarkChunk` bounds, retry and status (table test); `ExpiredUploads` skips finalizing; `DeleteUploadUnlessFinalizing`.

## Interfaces exposed
- `func (s *Store) DeleteUploadUnlessFinalizing(ctx context.Context, id string) (bool, error)`
- `func (s *Server) cancelUpload(ctx context.Context, up *store.Upload, reason string) bool` (was no return value; the existing statement call in `admin.go` compiles unchanged)
- New API error responses, all in the existing error envelope:
  - `PUT /uploads/{id}/chunks/{n}` can return 409 `upload_closed` where it used to return 500 or write silently;
  - `DELETE /uploads/{id}` returns 409 `upload_busy` while finalising.

## Commands run and results
- `go build ./...` OK
- `go vet ./internal/api/ ./internal/store/` OK
- `go test ./internal/api/ ./internal/store/` OK (`api_test.go` unchanged and passing)
- `gofmt -l` on the 4 owned files: clean. gofmt flags many other files in the repo for import ordering after the module rename; I left them alone because they aren't mine.
- `-race` not run (no cgo toolchain assumed on this host).

## Contract issues
None. The chunk endpoint is listed as "(existing)" in api-v1.md, and the new 409 codes use the standard error envelope.

## Gaps / follow-ups
- Web (browser) uploads send neither a file `sha256` nor `X-Chunk-SHA256` (`web/src/lib/uploads.tsx:97`), so the server still cannot verify them (second half of R3). The web client should send `X-Chunk-SHA256` (Web Crypto) and a file hash. That's a web task, not this one.
- `admin.go` maintenance still calls `cancelUpload` for open/finalizing rows from `ExpiredUploads`. That's now safe because finalizing rows are excluded and `cancelUpload` refuses them anyway. The `u.Status == "finalizing"` branch in `admin.go:647` is dead and can be simplified by P0-07 or INT.
- Admin cancel (`DELETE /admin/uploads/{id}`) on a finalising upload still returns 200 but does nothing; `admin.go` (P0-07) could check the bool and return 409 `upload_busy`.
- The locks are in-process only; that's sufficient for the single-process server.

## Notes for dependent tasks / INT
- No changes outside owned files; no new dependencies.
- `internal/store/uploads.go` now has LF line endings (gofmt).
