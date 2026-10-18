# P0-09 (SRV-5) Zip download missing-blob report (defect B2)
Status: completed

## Goal
`GET /api/v1/files/zip` silently dropped files whose blob was missing (no DB blob record, or blob file gone on disk). Make a missing blob never silent: a `MISSING_FILES.txt` entry in the archive, a `slog` warn, and an `X-VD-Missing-Count` header. The research doc (`01-server-backend.md` §8.4 row 4, W5 "R2") only says "fix the silent skip" and adds nothing else to follow.

## Changes
- vad-server: `internal/api/files.go` (modified)
  - `handleZip` now runs a **pre-flight pass before any header is written**. For each file entry it resolves `BlobLocation` and runs `os.Stat` on `BlobPath`. Missing entries are collected with a generic reason and skipped later.
  - The response sets `X-VD-Missing-Count: <n>` (always present, `0` when nothing is missing).
  - If a blob disappears between the pre-flight and `Open` (a race), it is still added to the manifest. The header was already sent, so it can undercount in that case only. This is documented in a code comment.
  - When anything is missing:
    - logs `slog.Warn("zip: blobs missing from download", "user", id, "missing", n, "entries", total)`, with no paths or contents;
    - appends a root-level `MISSING_FILES.txt` as the last entry: a header line, then one `path<TAB>reason` line per file.
  - The manifest name avoids collisions: if the archive already has a root entry `MISSING_FILES.txt`, it uses `MISSING_FILES-2.txt`, and so on.
  - Reasons (no server paths or raw error text leak):
    - `no blob record in the database`
    - `storage location unavailable`
    - `blob file not found on storage`
    - `blob file unreadable`
  - Refactor: the local `entry` type is hoisted to package-level `zipEntry`. New helpers are `zipMissing`, `(*Server).zipBlobLocation`, `zipMissingReason`, `zipManifestName` and `zipManifest`. The imports `io/fs` and `os` were added.
  - `gofmt -w` also reordered the pre-existing import block (`VaultDock/sdk` before `vad-server`); this is cosmetic.
- vad-server: `internal/api/zip_missing_test.go` (created). `TestZipReportsMissingBlobs` (table test, package `api_test`, uses the `api_test.go` helpers) does the following:
  - uploads 2 files through an enrolled device;
  - deletes one blob file on disk;
  - downloads the zip through the web session;
  - asserts the header count (`1` and `0`), the present file's bytes, and the manifest line `gone.txt\tblob file not found on storage`;
  - asserts there is no manifest when nothing is missing.

## Interfaces exposed
- Response header on `GET /api/v1/files/zip`: `X-VD-Missing-Count: <int>`.
- Archive entry `MISSING_FILES.txt` (root level, only when n > 0). Format: 2 header lines, a blank line, then `path\treason\n` for each missing file.

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./... && go vet ./internal/api/ && go test -count=1 ./internal/api/`: all ok.
- `go test -run Zip -v ./internal/api/`: PASS (both subtests).

## Contract issues
- `api-v1.md` does not document `X-VD-Missing-Count` or `MISSING_FILES.txt`. Both are additive. INT may want to note them in a future amendment.

## Gaps / follow-ups
- The header can undercount if a blob vanishes during streaming. The manifest is always complete.
- The web UI does not yet surface the header (for example as a toast). That would be a web task.
- The pre-flight costs one `stat` per file (up to 100k entries). This is negligible compared with streaming the contents.

## Notes for dependent tasks / INT
- If the web client starts reading `X-VD-Missing-Count` from a cross-origin context, it must be added to `Access-Control-Expose-Headers`. Same-origin use needs nothing.
- `files.go` is LF-only, as before; only owned files were touched.
