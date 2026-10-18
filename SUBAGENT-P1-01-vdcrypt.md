# P1-01 vdcrypt (SDK-REPO)
Status: completed

## Goal
Implement `sdk/vdcrypt` per backup-format-v1 rev 1.1 §3–§4, §12: subkeys, the epoch envelope, the X25519 sealed box, recipients and key files, the backward epoch chain and rotation, `VDRK1-` recovery text, and the passphrase KDF. Freeze test vectors 1, 6, 7 and 8 (§13).

## Changes (repo: VaultDock, all created)
- `sdk/vdcrypt/keys.go`: `Key`, `RepoID`, HKDF subkeys (DK → K_id/K_chunker/K_fh/K_mac; RMK_e → K_data_e/K_meta_e), X25519 helpers and clamping, the recovery recipient, SEK sign/verify and `OID`.
- `sdk/vdcrypt/envelope.go`: the `0x01 || u32(epoch) || nonce[24] || ct||tag` envelope, AD builders for every purpose, and raw seal/open used for wraps.
- `sdk/vdcrypt/box.go`: recipient kinds and the §4.2 sealed box (`Wrap`/`Unwrap`).
- `sdk/vdcrypt/keyfile.go`: the key-file JSON type with encode/decode/validate, full and meta bundles, Argon2id passphrase recipient, and escrow SIK/SEK verification.
- `sdk/vdcrypt/chain.go`: chain and meta-chain seal/open, `InitEpoch`, `Rotate`, `ResolveEpochs` and `ResolveMetaKeys`.
- `sdk/vdcrypt/recovery.go`: `VDRK1-` Crockford base32 encode/decode with checksum.
- `sdk/vdcrypt/vdcrypttest/vdcrypttest.go`: the §13 fixed seeds and the deterministic test RNG. It is exported so P1-03/04/05 vectors can reuse it.
- `sdk/vdcrypt/vectors_test.go`: generates and compares vectors 1, 6, 7 and 8. With `-update` it rewrites them; otherwise any byte difference fails the test. It also opens the frozen files independently of the generator.
- `sdk/vdcrypt/vdcrypt_test.go`: negative and round-trip tests: tamper (ciphertext, nonce, version, epoch field), wrong epoch, wrong key class, wrong AD, wrong blob id, wrong recipient/kind/repo/key_id, wrong passphrase, wrong SIK and SEK version, key-file field tampering, missing chain link, old epoch cannot open a newer chain, and bad recovery text and checksum.
- `sdk/repo/testdata/v1/crypto_subkeys.json` (vector 1), `crypto_keyfiles.json` (6), `crypto_chain.json` (7), `crypto_recovery.json` (8).

No new dependencies: the package uses stdlib (`crypto/hkdf`, `crypto/ed25519`, `crypto/sha256`) plus `golang.org/x/crypto` {chacha20poly1305, curve25519, argon2}, which is already in go.mod.

## Interfaces exposed (package `github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt`)
Every `rnd io.Reader` parameter means crypto/rand when nil.
```go
const KeySize = 32
type Key [32]byte            // Wipe(), Equal(Key) bool (constant-time), IsZero()
func KeyFromBytes(b []byte) (Key, error)
func RandomKey(rnd io.Reader) (Key, error)
type RepoID [16]byte         // String() canonical lowercase
func ParseRepoID(s string) (RepoID, error)   // lowercase canonical UUIDv4 only
type RepoKeys struct{ ID, Chunker, FileHash, ConfigMAC Key }  // Wipe()
func DeriveRepoKeys(repo RepoID, dk Key) RepoKeys
type EpochSecret struct{ Epoch uint32; RMK Key }  // Keys(repo) (EpochKeys, error), Wipe()
type EpochKeys struct{ Epoch uint32; Data, Meta Key } // Wipe()
func DeriveEpochKeys(repo RepoID, epoch uint32, rmk Key) (EpochKeys, error)
func Clamp(k Key) Key
func X25519Public(priv Key) (Key, error)
func X25519FromSeed(seed Key) (priv, pub Key, err error)
func GenerateX25519(rnd io.Reader) (priv, pub Key, err error)
func RecoveryPrivate(repo RepoID, secret Key) Key
func RecoveryKeyPair(repo RepoID, secret Key) (priv, pub Key, err error)
func SEKMessage(sekPub Key, version uint32) []byte
func SignSEK(sik ed25519.PrivateKey, sekPub Key, version uint32) []byte
func VerifySEK(sikPub ed25519.PublicKey, sekPub Key, version uint32, sig []byte) bool
func OID(stored []byte) string

// envelope
const EnvelopeVersion = 0x01; NonceSize = 24; TagSize = 16; EnvelopeOverhead = 45
const PurposeBlob, PurposePackHdr, PurposeIndex, PurposeSnapshot, PurposeLock, PurposeChain
func BlobAD(repo RepoID, blobID []byte) []byte
func PackHeaderAD(repo RepoID, packNonce []byte) []byte
func ObjectAD(purpose string, repo RepoID) ([]byte, error) // index|snapshot|lock|chain
func KeyWrapAD(repo RepoID, keyID string) []byte
func Seal(rnd io.Reader, key Key, epoch uint32, plaintext, ad []byte) ([]byte, error)
func EnvelopeEpoch(sealed []byte) (uint32, error)
func Open(key Key, epoch uint32, sealed, ad []byte) ([]byte, error)
// errors: ErrMalformed, ErrVersion, ErrEpochMismatch, ErrDecrypt, ErrPurpose, ErrInvalidEpoch, ErrInvalidKey, ErrInvalidRepoID

// sealed box
type Kind string; const KindDevice, KindEscrow, KindRecovery, KindPassphrase
func Wrap(rnd io.Reader, repo RepoID, kind Kind, recipientPub Key, keyID string, payload []byte) (epk Key, wrapped []byte, err error)
func Unwrap(repo RepoID, kind Kind, recipientPriv, epk Key, keyID string, wrapped []byte) ([]byte, error)

// key files
type PassphraseKDF struct{ Alg string; T, MKiB, P uint32; Salt []byte } // json alg,t,m_kib,p,salt
func NewPassphraseKDF(rnd io.Reader) (*PassphraseKDF, error) // t=3, m=64MiB, p=4, 16-byte salt
func PassphraseKeyPair(passphrase []byte, kdf *PassphraseKDF) (priv, pub Key, err error)
type Bundle struct{ Epoch uint32; Meta bool; RMK, DK, KMeta Key } // Secret(), Wipe()
func FullBundle(s EpochSecret, dk Key) Bundle
func MetaBundle(k EpochKeys) Bundle
type KeyFileDevice struct{ ID int64; SignPub []byte }
type KeyFileServer struct{ SIKPub, SEKSig []byte; SEKVersion uint32 }
type KeyFile struct{ V int; Repo, KeyID string; Kind Kind; Epoch uint32; Created int64; Label string;
  RecipientPub, EPK []byte; KDF *PassphraseKDF; Device *KeyFileDevice; Server *KeyFileServer; Wrapped []byte }
type KeyFileParams struct{ Repo RepoID; Kind Kind; Created int64; Label string; RecipientPub Key;
  KDF *PassphraseKDF; Device *KeyFileDevice; Server *KeyFileServer }
func NewKeyFile(rnd io.Reader, p KeyFileParams, b Bundle) (*KeyFile, error)
func DecodeKeyFile(data []byte) (*KeyFile, error)
func (kf *KeyFile) Encode() ([]byte, error)            // stored bytes; name = OID(bytes)
func (kf *KeyFile) Open(priv Key) (Bundle, error)
func (kf *KeyFile) OpenPassphrase(passphrase []byte) (Bundle, error)
func (kf *KeyFile) VerifyEscrow(pinnedSIK ed25519.PublicKey) error
// errors: ErrKeyFile, ErrBundle, ErrWrongRecipient, ErrPassphraseKDF, ErrEscrowSig, ErrKind

// chain / rotation
const MetaChainKind = "meta-chain"
type ChainLink struct{ Epoch uint32; MetaOnly, HasPrev bool; PrevRMK, PrevKMeta Key } // Wipe()
func SealChain(rnd io.Reader, repo RepoID, cur EpochSecret, prev *EpochSecret) ([]byte, error)
func SealMetaChain(rnd io.Reader, repo RepoID, cur EpochKeys, prevKMeta *Key) ([]byte, error)
func OpenChain(repo RepoID, epoch uint32, kMeta Key, sealed []byte) (ChainLink, error)
func InitEpoch(rnd io.Reader, repo RepoID) (EpochSecret, []byte, error)
func Rotate(rnd io.Reader, repo RepoID, cur EpochSecret) (next EpochSecret, chain []byte, err error)
func ResolveEpochs(repo RepoID, top EpochSecret, chains [][]byte) (map[uint32]EpochSecret, error)
func ResolveMetaKeys(repo RepoID, epoch uint32, kMeta Key, chains [][]byte) (map[uint32]Key, error)
// errors: ErrChain, ErrChainMissing

// recovery text
const RecoveryPrefix = "VDRK1-"
func NewRecoverySecret(rnd io.Reader) (Key, error)
func EncodeRecoveryKey(secret Key) string
func DecodeRecoveryKey(text string) (Key, error) // ErrRecoveryFormat, ErrRecoveryChecksum (user-facing)
```
Package `vdcrypt/vdcrypttest` provides `RepoUUID`, `Seq(start) [32]byte`, `DK`, `RMK1`, `RMK2`, `RecoverySecret`, `DeviceBoxSeed`, `SEKSeed`, `DeviceSignSeed`, `SIKSeed`, and `NewRNG(label) *RNG` (an io.Reader). Block i of the RNG stream is `SHA-256("vaultdock.testrng/1\x00" || label || u64le(i))`.

## Commands run and results
From `D:/VaultDock/VaultDock/sdk`:
- `gofmt -l vdcrypt`: clean, after `gofmt -w`.
- `go vet ./vdcrypt/...`: ok.
- `go test ./vdcrypt/... -run TestVectors -update`: generated the 4 vector files once.
- `go test -count=1 -v ./vdcrypt/`: all 10 tests pass, including `TestVectors` (recompute and byte-compare) and `TestVectorFilesOpen`.

## Contract issues
1. **Severe: the full chain leaks to metadata escrow.** §4.4 seals `chain_e`, which contains `prev_rmk`, under `K_meta_e`. Under `escrow_mode=metadata` the server holds `K_meta_e`, so it can open `chain_e` and obtain `RMK_{e-1}`, and with it `K_data_{e-1}` (data of every earlier epoch). That contradicts §1.2: "the server can never decrypt file contents". The implementation follows the literal contract (and the tests show `OpenChain` works with only `K_meta`). Suggested amendment: seal the full chain under `K_data_e`, or a dedicated `HKDF(RMK_e,…"chain")`, and keep only the meta-chain under `K_meta_e`. INT must decide before any metadata-escrow repository is written.
2. **Meta-chain JSON is unspecified.** The contract says only "kind `meta-chain`, containing only `prev_k_meta`". Implemented as `{"v":1,"kind":"meta-chain","epoch":e,"prev_k_meta":…}`. It is sealed under `K_meta_e` with AD `vd1:chain:<uuid>`, and epoch 1 has `prev_k_meta: null`. The full chain has no `kind` member, exactly as §4.4 shows.
3. **HKDF info strings.** `…"chunker"` and similar are read as `"vaultdock.repo/1 " + label`, matching the `"vaultdock.repo/1 id"` example. For the wrap, info is the literal byte concatenation `"vaultdock.repo/1 wrap" || kind || e_pub(32 raw) || R_pub(32 raw)`, with no separators. All HKDF outputs are 32 bytes.
4. **Passphrase NFC.** NFC needs `golang.org/x/text/unicode/norm`, which is not a dependency, and stdlib or x/crypto alone were mandated. `PassphraseKeyPair` requires the caller to pass an already NFC-normalised, valid-UTF-8 passphrase. Recommend that the dependency owner add `golang.org/x/text` (BSD-3) and normalise inside vdcrypt.
5. **Passphrase parameter bounds.** The contract gives `t≥3, m≥64 MiB, p=4`, yet vector 6 uses `t=1, m=8 MiB`. Writers default to t=3/64 MiB/p=4. Readers accept t 1–64, m 8 MiB–1 GiB, p 1–255 and a salt of 16–64 bytes, so the vector opens and hostile key files cannot exhaust memory. The salt length is unspecified; 16 bytes was chosen.
6. **Encoding details left open.**
   - Key-file and bundle JSON encoding: compact `encoding/json` with HTML escaping off and no trailing newline, field order as in §4.3. `label` is always present. `kdf`, `device` and `server` are omitted unless the kind requires them.
   - `key_id` = 16 random bytes as hex.
   - Seeds become X25519 private keys by clamping, and the clamped value is the stored private key.
   - In recovery text, the 34 bytes are encoded MSB-first into 55 chars, the last char zero-padded (11 groups). The decoder rejects non-zero pad bits and the Crockford-invalid `U`.
7. **Envelope header is not authenticated (minor).** The version and epoch bytes are not in the AD (§3.3 defines AD as the string only). Tampering with the epoch is still caught, because the reader then selects a different key, or `Open` reports `ErrEpochMismatch`.

## Gaps / follow-ups
- Vectors 2–5 and 9 belong to P1-03/04/05 and the config task. `vdcrypttest.NewRNG` and the seeds are there for them to reuse.
- No config MAC/JCS helper here: `K_mac` is exposed, and JCS belongs to the snapshot/config task.
- No SIK fingerprint format (api-v1 territory); only the SEK signature helpers.
- go.mod still lists `golang.org/x/crypto` as `// indirect`; INT's `go mod tidy` will mark it direct.

## Notes for dependent tasks / INT
- Choose the envelope key from `EnvelopeEpoch(sealed)`, then call `Open(key, epoch, …)`. Blobs use `BlobAD`, pack headers use `PackHeaderAD`, and index/snapshot/lock use `ObjectAD`.
- Server (P1-12): use `SignSEK`, `VerifySEK` and `KeyFile.VerifyEscrow`. Escrow meta bundles come from `MetaBundle(EpochKeys)`. After rotation under metadata escrow, also write `SealMetaChain`.
- To regenerate vectors (format change only, i.e. never in v1): `go test ./vdcrypt/ -run TestVectors -update`.

## Follow-up: rev 1.2
Requested by INT after contract issue 1 (chain leaks to metadata escrow) was fixed in backup-format-v1 rev 1.2 (§3.2, §3.3, §4.4, §14). Status: completed.

**Changes (all in owned files)**
- `vdcrypt/keys.go`: `EpochKeys` gains `Chain Key` = `HKDF(RMK_e, salt=uuid_bytes||u32(e), info="vaultdock.repo/1 chain")`. `Wipe` zeroes it as well.
- `vdcrypt/envelope.go`: adds `PurposeMetaChain = "metachain"`. `ObjectAD` accepts it.
- `vdcrypt/chain.go`: rewritten.
  - `chain_e = seal(K_chain_e, AD "vd1:chain:<uuid>", {"v":1,"epoch":e,"prev_rmk":…|null})`.
  - `metachain_e = seal(K_meta_e, AD "vd1:metachain:<uuid>", {"v":1,"epoch":e,"prev_k_meta":…|null})`.
  - The full chain no longer contains `prev_k_meta`, and the `kind` member is gone.
  - Payload parsing is strict: exactly `v`, `epoch` and the single prev field; `null` only for epoch 1.
- Tests:
  - Vector 1 now includes `k_chain` for epochs 1 and 2.
  - Vector 7 now holds `chain_1`, `chain_2`, `metachain_1` and `metachain_2`, each with its rng label, plaintext, AD, sealed hex and oid.
  - The old test that opened the chain with only `K_meta` is replaced by `TestChainAndRotation`. It proves a `K_meta`-only holder gets `ErrDecrypt` on every `chain_e` but can walk back to epoch 1 with `WalkMeta`, and that `WalkMeta` fails when it is given only full chain objects.
  - `TestVectorFilesOpen` also asserts that the frozen `chain_2` does not open with `K_meta_2` and that `metachain_2` links to `K_meta_1`.
- Vectors 6 (key files) and 8 (recovery) are unchanged.

**API changes (replace the chain block above)**
```go
type EpochKeys struct{ Epoch uint32; Data, Meta, Chain Key }
const PurposeMetaChain = "metachain"
type ChainLink struct{ Epoch uint32; HasPrev bool; PrevRMK Key }         // Wipe()
type MetaChainLink struct{ Epoch uint32; HasPrev bool; PrevKMeta Key }   // Wipe()
type EpochObjects struct{ Chain, MetaChain []byte }
func SealChain(rnd io.Reader, repo RepoID, cur EpochSecret, prev *EpochSecret) ([]byte, error)
func SealMetaChain(rnd io.Reader, repo RepoID, cur EpochKeys, prevKMeta *Key) ([]byte, error)
func OpenChain(repo RepoID, epoch uint32, kChain Key, sealed []byte) (ChainLink, error)
func OpenMetaChain(repo RepoID, epoch uint32, kMeta Key, sealed []byte) (MetaChainLink, error)
func InitEpoch(rnd io.Reader, repo RepoID) (EpochSecret, EpochObjects, error)       // writes both objects
func Rotate(rnd io.Reader, repo RepoID, cur EpochSecret) (EpochSecret, EpochObjects, error) // writes both
func ResolveEpochs(repo RepoID, top EpochSecret, chains [][]byte) (map[uint32]EpochSecret, error) // K_chain walk
func WalkMeta(repo RepoID, epoch uint32, kMeta Key, chains [][]byte) (map[uint32]Key, error)     // K_meta-only walk
```
Removed: `MetaChainKind`, `ChainLink.MetaOnly`, `ChainLink.PrevKMeta`, `ResolveMetaKeys` (replaced by `WalkMeta`).

**Commands**
From `sdk/`:
- `gofmt -l vdcrypt`: clean.
- `go vet ./vdcrypt/`: ok.
- `go test ./vdcrypt/ -run TestVectors -update`: regenerated vectors 1 and 7.
- `go test -count=1 ./vdcrypt/`: all 10 tests pass.

**Contract issues (rev 1.2)**
- The header Status line of `backup-format-v1.md` still says "contract revision 1.1", while §14 has the rev 1.2 entry. This is cosmetic; INT should update it.
- §3.3's prose still says the key is "`K_data_e` or `K_meta_e`", although the table now also uses `K_chain_e`. I followed the table.
- §4.5 rotation step 2 names only `chain_{e+1}`. Following §4.4 ("both written for every epoch"), `Rotate` and `InitEpoch` return both objects.
- Contract issue 1 above is resolved by rev 1.2. Issues 2 (meta-chain JSON) and 3 (chain part of the HKDF/kind reading) are superseded by the explicit rev 1.2 definitions.
