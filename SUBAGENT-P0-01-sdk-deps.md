# P0-01 SDK dependencies (done by INT)
Status: completed
## Goal
Add the SDK's planned third-party dependencies.
## Changes
- VaultDock: `sdk/go.mod`, `sdk/go.sum`: modified. Added `github.com/klauspost/compress v1.20.3` (BSD-3-Clause; zstd for `sdk/compress`) and `golang.org/x/crypto v0.58.0` (BSD-3-Clause; XChaCha20-Poly1305, X25519, HKDF, Argon2 for `sdk/vdcrypt`). Both stay `// indirect` until P1-01/P1-02 import them; INT runs `go mod tidy` at review.
- VaultDock: `sdk/wire/common.go`: created by INT. Shared wire types from api-v1 §1: error envelope and codes, feature flags, `List[T]`, bulk/operation/approval/plan replies, `SecretState`, tri-state `SecretUpdate`. Created ahead of P0-08a/b so both halves can run in parallel.
## Commands run and results
`go get …` succeeded; `go vet ./wire/` is clean.
## Contract issues
None.
## Notes for dependent tasks / INT
P1-01 and P1-02 may import these without further `go get`.
