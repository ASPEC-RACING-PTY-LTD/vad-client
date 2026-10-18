package keystore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// fakeProtector XORs with a fixed pad and checks the entropy.
type fakeProtector struct{}

func (fakeProtector) Protect(data, entropy []byte) ([]byte, error) {
	out := append([]byte("FAKE"), entropy...)
	out = append(out, 0)
	for _, b := range data {
		out = append(out, b^0x5a)
	}
	return out, nil
}

func (fakeProtector) Unprotect(data, entropy []byte) ([]byte, error) {
	prefix := append(append([]byte("FAKE"), entropy...), 0)
	if !bytes.HasPrefix(data, prefix) {
		return nil, errors.New("fake: wrong entropy or not protected")
	}
	out := make([]byte, 0, len(data)-len(prefix))
	for _, b := range data[len(prefix):] {
		out = append(out, b^0x5a)
	}
	return out, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newKS(t *testing.T, dir string, opts Options) *Keystore {
	t.Helper()
	opts.Dir, opts.Protector, opts.Logger = dir, fakeProtector{}, quiet
	ks, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ks.Close)
	return ks
}

// testServer is a server identity: SIK (Ed25519) and SEK (X25519).
type testServer struct {
	sik    ed25519.PrivateKey
	sekPub vdcrypt.Key
}

func newServer(t *testing.T) *testServer {
	t.Helper()
	_, sik, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, sekPub, err := vdcrypt.GenerateX25519(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testServer{sik: sik, sekPub: sekPub}
}

func (s *testServer) identity() *wire.ServerIdentity {
	pub := s.sik.Public().(ed25519.PublicKey)
	return &wire.ServerIdentity{
		SIKPub:         base64.StdEncoding.EncodeToString(pub),
		SIKFingerprint: Fingerprint(pub),
		SEKPub:         base64.StdEncoding.EncodeToString(s.sekPub[:]),
		SEKVersion:     1,
		SEKSig:         base64.StdEncoding.EncodeToString(vdcrypt.SignSEK(s.sik, s.sekPub, 1)),
	}
}

func TestBoxKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := newKS(t, dir, Options{})
	pub := a.BoxPub()
	if pub.IsZero() {
		t.Fatal("zero box pub")
	}
	if got, _ := base64.StdEncoding.DecodeString(a.BoxPubB64()); !bytes.Equal(got, pub[:]) {
		t.Fatal("BoxPubB64 mismatch")
	}
	b := newKS(t, dir, Options{})
	if !b.BoxPub().Equal(pub) {
		t.Fatal("reopen produced a different box key")
	}
	raw, err := os.ReadFile(filepath.Join(dir, BoxKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, a.boxPriv[:]) {
		t.Fatal("box private key stored in clear")
	}
	if err := os.WriteFile(filepath.Join(dir, BoxKeyFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir, Protector: fakeProtector{}, Logger: quiet}); err == nil {
		t.Fatal("corrupt box key opened")
	}
}

func TestFingerprint(t *testing.T) {
	pub := bytes.Repeat([]byte{7}, ed25519.PublicKeySize)
	fp := Fingerprint(pub)
	if !regexp.MustCompile(`^([0-9A-F]{4}-){7}[0-9A-F]{4}$`).MatchString(fp) {
		t.Fatalf("format: %q", fp)
	}
	sum := sha256.Sum256(pub)
	if strings.ReplaceAll(fp, "-", "") != fmt.Sprintf("%X", sum[:16]) {
		t.Fatalf("value: %q", fp)
	}
}

func TestPin(t *testing.T) {
	dir := t.TempDir()
	ks := newKS(t, dir, Options{})
	srv, other := newServer(t), newServer(t)

	if _, err := ks.Verify(srv.identity()); !errors.Is(err, ErrNotPinned) {
		t.Fatalf("verify before pin: %v", err)
	}
	if _, err := ks.PinServerIdentity(srv.identity()); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.PinServerIdentity(srv.identity()); err != nil {
		t.Fatalf("re-pin same identity: %v", err)
	}
	lower := srv.identity()
	lower.SIKFingerprint = strings.ToLower(strings.ReplaceAll(lower.SIKFingerprint, "-", ":"))
	if _, err := ks.Verify(lower); err != nil {
		t.Fatalf("normalised fingerprint: %v", err)
	}

	forged := other.identity()
	forged.SIKFingerprint = srv.identity().SIKFingerprint // claims the pinned fingerprint
	badSig := srv.identity()
	badSig.SEKSig = other.identity().SEKSig
	cases := []struct {
		name     string
		id       *wire.ServerIdentity
		mismatch bool
	}{
		{"different server", other.identity(), true},
		{"forged fingerprint", forged, true},
		{"bad sek signature", badSig, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ks.Verify(c.id)
			if err == nil {
				t.Fatal("accepted")
			}
			var me *MismatchError
			if c.mismatch != errors.As(err, &me) || c.mismatch != client.IsCode(err, wire.CodeSIKMismatch) {
				t.Fatalf("mismatch=%v: %v", c.mismatch, err)
			}
		})
	}
	if _, err := ks.PinServerIdentity(other.identity()); !client.IsCode(err, wire.CodeSIKMismatch) {
		t.Fatalf("TOFU overwrite: %v", err)
	}

	// The pin survives a reopen; Repin replaces it.
	ks2 := newKS(t, dir, Options{})
	if ks2.Pinned() != Fingerprint(srv.sik.Public().(ed25519.PublicKey)) {
		t.Fatalf("pinned after reopen: %q", ks2.Pinned())
	}
	if _, err := ks2.Repin(other.identity()); err != nil {
		t.Fatal(err)
	}
	if _, err := ks2.Verify(other.identity()); err != nil {
		t.Fatal(err)
	}
	if _, err := ks2.Verify(srv.identity()); !client.IsCode(err, wire.CodeSIKMismatch) {
		t.Fatalf("old server after repin: %v", err)
	}

	// A pin file whose fingerprint does not match its key is rejected.
	var p pinRecord
	b, _ := os.ReadFile(filepath.Join(dir, PinFile))
	_ = json.Unmarshal(b, &p)
	p.Fingerprint = Fingerprint(srv.sik.Public().(ed25519.PublicKey))
	b, _ = json.Marshal(p)
	if err := os.WriteFile(filepath.Join(dir, PinFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir, Protector: fakeProtector{}, Logger: quiet}); !errors.Is(err, ErrPinFile) {
		t.Fatalf("tampered pin: %v", err)
	}
}

type device struct {
	ks      *Keystore
	id      int64
	signPub ed25519.PublicKey
	dir     string
}

func newDevice(t *testing.T, id int64, srv *testServer, opts Options) *device {
	t.Helper()
	dir := t.TempDir()
	ks := newKS(t, dir, opts)
	if _, err := ks.PinServerIdentity(srv.identity()); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &device{ks: ks, id: id, signPub: pub, dir: dir}
}

// newRepoID returns a random UUIDv4 repository id.
func newRepoID(t *testing.T) vdcrypt.RepoID {
	t.Helper()
	var id vdcrypt.RepoID
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id
}

func initRepo(t *testing.T, d *device, srv *testServer, be repo.Backend, mode string) (*repo.Repo, string) {
	t.Helper()
	r, rk, err := d.ks.InitRepo(context.Background(), InitParams{
		Backend: be, RepoID: newRepoID(t), EscrowMode: mode, DeviceID: d.id, SignPub: d.signPub, Server: srv.identity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, rk.Text()
}

func TestInitRepo(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	a := newDevice(t, 1, srv, Options{})
	be := repo.NewMemBackend()

	// A server identity that does not match the pin is refused.
	if _, _, err := a.ks.InitRepo(ctx, InitParams{Backend: be, RepoID: newRepoID(t), DeviceID: 1, SignPub: a.signPub,
		Server: newServer(t).identity()}); !client.IsCode(err, wire.CodeSIKMismatch) {
		t.Fatalf("init with wrong server: %v", err)
	}
	// A zero repository id is refused before anything is written.
	if _, _, err := a.ks.InitRepo(ctx, InitParams{Backend: be, DeviceID: 1, SignPub: a.signPub,
		Server: srv.identity()}); !errors.Is(err, ErrNoRepoID) {
		t.Fatalf("init without repo id: %v", err)
	}
	if _, err := be.Stat(ctx, repo.KindConfig, repo.ConfigOID); err == nil {
		t.Fatal("init without repo id wrote a config")
	}

	r, text := initRepo(t, a, srv, be, repo.EscrowFull)
	if !strings.HasPrefix(text, vdcrypt.RecoveryPrefix) {
		t.Fatalf("recovery text %q", text)
	}
	if err := CheckRecoveryKey(r, text); err != nil {
		t.Fatal(err)
	}
	other, _ := vdcrypt.NewRecoverySecret(rand.Reader)
	if err := CheckRecoveryKey(r, vdcrypt.EncodeRecoveryKey(other)); !errors.Is(err, ErrRecoveryMismatch) {
		t.Fatalf("wrong recovery key: %v", err)
	}

	// The text is never persisted.
	_ = filepath.WalkDir(a.dir, func(p string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(text)) {
				t.Errorf("recovery text found in %s", p)
			}
		}
		return nil
	})

	r2, err := a.ks.OpenRepo(ctx, be)
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID() != r.ID() || r2.MetadataOnly() {
		t.Fatal("device open")
	}
	r2.Close()
	cred, err := repo.RecoveryCredential(text)
	if err != nil {
		t.Fatal(err)
	}
	r3, err := repo.Open(ctx, be, cred, repo.Options{Logger: quiet})
	if err != nil {
		t.Fatalf("recovery open: %v", err)
	}
	r3.Close()

	// After a re-pin to another server the escrow key file no longer
	// verifies, so the device refuses to open.
	if _, err := a.ks.Repin(newServer(t).identity()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ks.OpenRepo(ctx, be); !errors.Is(err, vdcrypt.ErrEscrowSig) {
		t.Fatalf("escrow under foreign pin: %v", err)
	}
}

func TestRecoveryKeyRedacted(t *testing.T) {
	text := "VDRK1-SECRET"
	rk := RecoveryKey{text: &text}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "rk", rk)
	j, _ := json.Marshal(struct{ K RecoveryKey }{rk})
	for _, s := range []string{fmt.Sprint(rk), fmt.Sprintf("%v %+v %#v %s %q", rk, rk, rk, rk, rk), buf.String(), string(j)} {
		if strings.Contains(s, "SECRET") {
			t.Fatalf("leaked: %s", s)
		}
	}
	if rk.Text() != text {
		t.Fatal("Text")
	}
	rk.Wipe()
	if rk.Text() != "" {
		t.Fatal("Wipe")
	}
}

func task(t *testing.T, kind string, p any) wire.DeviceTask {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return wire.DeviceTask{ID: 9, Kind: kind, Payload: raw}
}

func backendHook(be repo.Backend) func(context.Context, int64) (repo.Backend, error) {
	return func(context.Context, int64) (repo.Backend, error) { return be, nil }
}

func shareTask(t *testing.T, target *device) wire.DeviceTask {
	pub := target.ks.BoxPub()
	return task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{
		RepoID: 5, TargetDeviceID: target.id,
		TargetBoxPub:  base64.StdEncoding.EncodeToString(pub[:]),
		TargetSignPub: base64.StdEncoding.EncodeToString(target.signPub),
	})
}

func TestKeyShareMetadataAndRewrap(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	be := repo.NewMemBackend()
	revoked := map[int64]bool{}
	opts := Options{Backend: backendHook(be), Revoked: func(context.Context) (map[int64]bool, error) { return revoked, nil }}
	a := newDevice(t, 1, srv, opts)
	b := newDevice(t, 2, srv, opts)
	initRepo(t, a, srv, be, repo.EscrowMetadata)

	if _, err := b.ks.OpenRepo(ctx, be); !errors.Is(err, repo.ErrNoKeyFile) {
		t.Fatalf("b before share: %v", err)
	}
	out, err := a.ks.HandleTask(ctx, shareTask(t, b))
	if err != nil {
		t.Fatal(err)
	}
	res := out.(client.KeyTaskResult)
	if res.RepoID != 5 || res.Epoch != 1 || !repo.IsOID(res.KeyFileOID) || res.Error != "" {
		t.Fatalf("share result %+v", res)
	}
	out2, err := a.ks.HandleTask(ctx, shareTask(t, b))
	if err != nil || out2.(client.KeyTaskResult).KeyFileOID != res.KeyFileOID {
		t.Fatalf("idempotent share: %+v %v", out2, err)
	}
	rb, err := b.ks.OpenRepo(ctx, be)
	if err != nil {
		t.Fatalf("b after share: %v", err)
	}
	rb.Close()

	// Revoke b; rewrap to epoch 2 excludes it.
	revoked[2] = true
	if _, err := a.ks.HandleTask(ctx, shareTask(t, b)); !errors.Is(err, ErrTargetRevoked) {
		t.Fatalf("share to revoked: %v", err)
	}
	out, err = a.ks.HandleTask(ctx, task(t, wire.TaskKindRewrap, wire.TaskRewrapPayload{RepoID: 5, Epoch: 2}))
	if err != nil || out.(client.KeyTaskResult).Epoch != 2 {
		t.Fatalf("rewrap: %+v %v", out, err)
	}
	out, err = a.ks.HandleTask(ctx, task(t, wire.TaskKindRewrap, wire.TaskRewrapPayload{RepoID: 5, Epoch: 2}))
	if err != nil || out.(client.KeyTaskResult).Epoch != 2 {
		t.Fatalf("rewrap again: %+v %v", out, err)
	}
	if _, err := a.ks.HandleTask(ctx, task(t, wire.TaskKindRewrap, wire.TaskRewrapPayload{RepoID: 5, Epoch: 4})); err == nil {
		t.Fatal("rewrap skipping an epoch")
	}
	rb, err = b.ks.OpenRepo(ctx, be)
	if err != nil {
		t.Fatal(err)
	}
	if !rb.Stale() || rb.Epoch() != 1 {
		t.Fatalf("revoked device: stale=%v epoch=%d", rb.Stale(), rb.Epoch())
	}
	rb.Close()
	ra, err := a.ks.OpenRepo(ctx, be)
	if err != nil || ra.Epoch() != 2 || ra.Stale() || !ra.RecoveryReady() {
		t.Fatalf("a after rewrap: %v", err)
	}
	ra.Close()
}

func TestKeyShareFullEscrow(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	be := repo.NewMemBackend()
	a := newDevice(t, 1, srv, Options{Backend: backendHook(be)})
	b := newDevice(t, 2, srv, Options{Backend: backendHook(be)})
	r, _ := initRepo(t, a, srv, be, repo.EscrowFull)

	// Stand in for the server writing b's device wrap under full escrow.
	before := r.KeyFiles()
	if err := r.AddRecipient(ctx, repo.DeviceRecipient(b.ks.BoxPub(), b.id, b.signPub, "b")); err != nil {
		t.Fatal(err)
	}
	var oid, aOID string
	for k, kf := range r.KeyFiles() {
		if _, ok := before[k]; !ok {
			oid = k
		} else if kf.Kind == vdcrypt.KindDevice {
			aOID = k
		}
	}
	out, err := b.ks.HandleTask(ctx, task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{RepoID: 5, KeyFileOID: oid}))
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(client.KeyTaskResult); res.Epoch != 1 || res.KeyFileOID != oid {
		t.Fatalf("result %+v", res)
	}

	bad := []struct {
		name string
		t    wire.DeviceTask
	}{
		{"other device's key file", task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{RepoID: 5, KeyFileOID: aOID})},
		{"bad oid", task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{RepoID: 5, KeyFileOID: "xyz"})},
		{"both shapes", task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{RepoID: 5, KeyFileOID: oid, TargetDeviceID: 3})},
		{"no repo", task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{KeyFileOID: oid})},
		{"unknown field", wire.DeviceTask{Kind: wire.TaskKindKeyShare, Payload: []byte(`{"repo_id":5,"x":1}`)}},
		{"bad target key", task(t, wire.TaskKindKeyShare, wire.TaskKeySharePayload{RepoID: 5, TargetDeviceID: 3, TargetBoxPub: "AA==", TargetSignPub: "AA=="})},
		{"not a key task", task(t, wire.TaskKindBackupNow, wire.TaskBackupNowPayload{JobID: 1})},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			out, err := b.ks.HandleTask(ctx, c.t)
			if err == nil || out.(client.KeyTaskResult).Error == "" {
				t.Fatalf("accepted: %+v", out)
			}
		})
	}
}
