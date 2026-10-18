package e2e

// End-to-end backup through the desktop engine (P1-36): keystore enrolment
// and SIK pin → first-device repository init → backup_now task → runner →
// pipeline → commit → catalog browse → incremental run → a second device
// handles its key_share task and restores the snapshot. Everything runs
// in-process against testkit, with temporary directories, a fake token
// provider and box-key protector, and no elevation.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/keystore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/protocol"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo/backend/server"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
	"github.com/ASPEC-RACING-PTY-LTD/vad-server/testkit"
)

const (
	e2eSID       = "S-1-5-21-1-2-3-1001"
	e2eConsentID = "c1"
	e2eRepoUUID  = "6c4f6c1e-3d2a-4f5b-8c9d-0e1f2a3b4c5d"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// plainProtector stands in for machine DPAPI (which seals per machine and
// would tie the test to Windows).
type plainProtector struct{}

func (plainProtector) Protect(data, _ []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}
func (plainProtector) Unprotect(data, _ []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

// selfToken is a session-like token for the test process's own user;
// Impersonate just runs fn (no SYSTEM, no WTSQueryUserToken).
type selfToken struct{}

func (selfToken) SID() string                                  { return e2eSID }
func (selfToken) Source() tokens.Source                        { return tokens.SourceSession }
func (selfToken) SessionID() (uint32, bool)                    { return 1, true }
func (selfToken) Caps() tokens.Caps                            { return tokens.CapsFor(tokens.SourceSession) }
func (selfToken) Impersonate(fn func() error) error            { return fn() }
func (selfToken) AccessCheckPath(string, uint32) (bool, error) { return true, nil }
func (selfToken) Close() error                                 { return nil }

type selfProvider struct{}

func (selfProvider) ForProfile(string) (tokens.Token, error)  { return selfToken{}, nil }
func (selfProvider) SessionOnly(string) (tokens.Token, error) { return selfToken{}, nil }

// docsFolder resolves {DOCUMENTS} to a temporary directory.
type docsFolder struct{ dir string }

func (f docsFolder) Tokens(context.Context, guard.Profile) (snapshot.Tokens, error) {
	return snapshot.Tokens{snapshot.TokenDocuments: f.dir}, nil
}

// agent is one simulated desktop service: keystore, signed client, local
// backup store, runner and task loop.
type agent struct {
	name   string
	c      *client.Client
	sign   ed25519.PrivateKey
	ks     *keystore.Keystore
	st     *backup.Store
	runner *backup.Runner
}

// newAgent enrols a device with the keystore's box key, approves it with
// the admin session and pins the server identity from the approved poll.
func newAgent(t *testing.T, srv *testkit.Server, name string) *agent {
	t.Helper()
	ctx := context.Background()
	base, _ := url.Parse(srv.URL)
	a := &agent{name: name, c: client.New(base, client.Options{})}
	ks, err := keystore.Open(keystore.Options{Dir: t.TempDir(), Protector: plainProtector{}, Logger: quiet,
		Backend: func(_ context.Context, repoID int64) (repo.Backend, error) {
			return server.New(a.c, repoID, server.Options{Logger: quiet}), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ks.Close)
	a.ks = ks
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	res, err := a.c.Enroll(ctx, client.EnrollRequest{Login: "admin", Password: testkit.Password, DeviceName: name, OS: "Windows 11",
		Identifier: name, PublicKey: protocol.EncodePublicKey(pub), ClientVersion: "e2e", BoxPub: ks.BoxPubB64(),
		InstallMode: "simple", CoreVersion: "e2e"})
	if err != nil {
		t.Fatalf("enrol %s: %v", name, err)
	}
	srv.Do("POST", fmt.Sprintf("/api/v1/devices/%d/approve", res.RequestID), nil, 200)
	est, err := a.c.EnrollStatus(ctx, res.RequestID, res.PollToken)
	if err != nil || est.Status != "approved" || est.ServerIdentity == nil {
		t.Fatalf("enrol %s status: %+v %v", name, est, err)
	}
	a.c.DeviceID, a.c.Key, a.sign = est.DeviceID, priv, priv
	ident, err := ks.PinServerIdentity(est.ServerIdentity)
	if err != nil || ks.Pinned() != est.ServerIdentity.SIKFingerprint || ident.Fingerprint != ks.Pinned() {
		t.Fatalf("pin: %+v %v", ident, err)
	}
	if a.st, err = backup.OpenDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.st.Close() })
	return a
}

// serve starts the runner and the device task loop until the test ends.
func (a *agent) serve(t *testing.T, docs string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	base := &backup.Pipeline{Store: a.st, SpoolDir: filepath.Join(t.TempDir(), "spool"), Tokens: selfProvider{},
		Walker:  backup.Walker{Guard: func(guard.Op, string, guard.Profile) error { return nil }, Log: quiet},
		Folders: docsFolder{dir: docs}, PackTarget: 256 << 10, Log: quiet}
	engines := backup.RepoEngines(base, func(ctx context.Context, id int64) (*repo.Repo, error) {
		return a.ks.OpenRepo(ctx, server.New(a.c, id, server.Options{Logger: quiet}))
	})
	a.runner = &backup.Runner{Store: a.st, API: a.c, Engines: engines, Log: quiet,
		HeartbeatEvery: 100 * time.Millisecond, IssueFlushEvery: 100 * time.Millisecond,
		Profiles: func(context.Context, string) (backup.ProfileInfo, error) {
			return backup.ProfileInfo{SID: e2eSID, Username: "e2e", ProfilePath: filepath.Dir(docs), ConsentID: e2eConsentID, Allowed: true}, nil
		},
		Device: backup.Identity{DeviceID: a.c.DeviceID, DeviceName: a.name, Key: a.sign, Hostname: a.name, InstallMode: "simple"}}
	loop := &backup.TaskLoop{API: a.c, Backups: a.runner, Keys: a.ks, Wait: time.Second,
		MinBackoff: 50 * time.Millisecond, MaxBackoff: 500 * time.Millisecond, Log: quiet}
	done := make(chan struct{})
	go func() { _ = loop.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		a.runner.Wait()
	})
}

func (a *agent) reportConsent(t *testing.T, docs string) {
	t.Helper()
	rep := wire.ProfileReport{
		Profiles: []wire.ProfileReportEntry{{SID: e2eSID, Username: "e2e", ProfilePath: filepath.Dir(docs), State: wire.ProfileStateActiveDefault}},
		Consents: []wire.ProfileConsentRecord{{ConsentID: e2eConsentID, SID: e2eSID, ConsentAt: 1000, ProofDigest: strings.Repeat("a", 64)}},
	}
	if err := a.c.ReportProfiles(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
}

// backupNow queues backup_now through the admin session and waits for the
// local run it starts to finish.
func backupNow(t *testing.T, srv *testkit.Server, a *agent, jobID int64, runsBefore int) backup.Run {
	t.Helper()
	srv.Do("POST", "/api/v1/device/tasks", map[string]any{"device_id": a.c.DeviceID, "kind": wire.TaskKindBackupNow,
		"payload": map[string]any{"job_id": jobID}}, 201)
	var run backup.Run
	waitFor(t, 60*time.Second, "backup run", func() bool {
		runs, err := a.st.Runs(context.Background(), jobID, 0)
		if err != nil || len(runs) <= runsBefore || runs[0].FinishedAt == 0 {
			return false
		}
		run = runs[0]
		return true
	})
	if run.Status != wire.RunStatusCompleted || run.SnapshotID == 0 || run.RunID == 0 {
		t.Fatalf("run: %+v", run)
	}
	return run
}

func waitBrowsable(t *testing.T, srv *testkit.Server, id int64) map[string]any {
	t.Helper()
	var sn map[string]any
	waitFor(t, 20*time.Second, "catalog ingest", func() bool {
		sn = srv.Do("GET", fmt.Sprintf("/api/v1/snapshots/%d", id), nil, 200)
		if sn["ingest_state"] == wire.SnapshotIngestFailed {
			t.Fatalf("ingest failed: %v", sn)
		}
		return sn["ingest_state"] == wire.SnapshotIngestDone
	})
	return sn
}

func listNames(out map[string]any) string {
	var ns []string
	items, _ := out["items"].([]any)
	for _, it := range items {
		ns = append(ns, it.(map[string]any)["name"].(string))
	}
	return strings.Join(ns, "|")
}

// diskFiles reads the backed-up tree as {DOCUMENTS}-relative slash paths.
func diskFiles(t *testing.T, root string, skip func(string) bool) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if skip(rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// restore reads every file of a snapshot through r, checking the keyed
// file hash and size, and the root's device signature.
func restore(t *testing.T, r *repo.Repo, oid string) (*snapshot.Root, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	root, stored, err := r.LoadSnapshot(ctx, oid)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if err := r.VerifySnapshot(root, stored); err != nil {
		t.Fatalf("verify snapshot: %v", err)
	}
	rk, err := r.Keys()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	var walk func(prefix, tree string)
	walk = func(prefix, tree string) {
		for tree != "" {
			tr, err := r.ReadTree(ctx, tree)
			if err != nil {
				t.Fatalf("tree %s: %v", prefix, err)
			}
			for _, e := range tr.Entries {
				p := strings.TrimPrefix(prefix+"/"+e.N, "/")
				switch e.T {
				case snapshot.TypeDir:
					if e.Sub != nil {
						walk(p, *e.Sub)
					}
					continue
				case snapshot.TypeFile:
				default:
					continue
				}
				var buf []byte
				for _, c := range e.Ch {
					raw, _ := hex.DecodeString(c)
					b, err := r.ReadBlob(ctx, pack.BlobID(raw))
					if err != nil {
						t.Fatalf("blob of %s: %v", p, err)
					}
					buf = append(buf, b...)
				}
				if e.H == nil || !snapshot.VerifyFileHash(snapshot.FileHashBytes(rk.FileHash, buf), *e.H) || int64(len(buf)) != e.S {
					t.Errorf("%s: hash or size mismatch", p)
				}
				out[p] = buf
			}
			tree = ""
			if tr.Cont != nil {
				tree = *tr.Cont
			}
		}
	}
	for _, rr := range root.Roots {
		if rr.TokenPath != snapshot.TokenDocuments {
			t.Fatalf("unexpected root %q", rr.TokenPath)
		}
		walk("", rr.Tree)
	}
	return root, out
}

func sameFiles(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	names := func(m map[string][]byte) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, "|")
	}
	if names(got) != names(want) {
		t.Fatalf("restored %s, want %s", names(got), names(want))
	}
	for p, b := range want {
		if !bytes.Equal(got[p], b) {
			t.Errorf("%s: restored content differs", p)
		}
	}
}

func TestBackupEngineEndToEnd(t *testing.T) {
	srv := testkit.Start(t)
	ctx := context.Background()
	docs := filepath.Join(t.TempDir(), "Documents")
	writeFile(t, filepath.Join(docs, "Quarterly Report 2024.pdf"), 1<<20, time.Hour)
	writeFile(t, filepath.Join(docs, "notes.txt"), 2000, time.Hour)
	writeFile(t, filepath.Join(docs, "Projects", "vaultdock-design.md"), 5000, time.Hour)
	writeFile(t, filepath.Join(docs, "Projects", "scratch.tmp"), 3000, time.Hour) // excluded
	isExcluded := func(rel string) bool { return strings.HasSuffix(rel, ".tmp") }

	// Device A: enrolment and SIK pin, first-device keys.
	a := newAgent(t, srv, "PC-A")
	ident, err := a.c.ServerIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ks.Verify(ident); err != nil {
		t.Fatalf("verify pinned identity: %v", err)
	}
	rp, err := a.c.CreateRepo(ctx, wire.RepoCreateRequest{RepoUUID: e2eRepoUUID, Name: "PC-A backups", EscrowMode: repo.EscrowFull})
	if err != nil {
		t.Fatal(err)
	}
	rid, err := vdcrypt.ParseRepoID(e2eRepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	r, rk, err := a.ks.InitRepo(ctx, keystore.InitParams{Backend: server.New(a.c, rp.ID, server.Options{Logger: quiet}), RepoID: rid, EscrowMode: repo.EscrowFull,
		DeviceID: a.c.DeviceID, SignPub: a.sign.Public().(ed25519.PublicKey), Label: a.name, Server: ident})
	if err != nil {
		t.Fatalf("init repo: %v", err)
	}
	if err := keystore.CheckRecoveryKey(r, rk.Text()); err != nil {
		t.Fatalf("recovery key: %v", err)
	}
	rk.Wipe()
	r.Close()
	if ks, err := a.c.KeyStatus(ctx, rp.ID); err != nil || ks.CurrentEpoch != 1 || len(ks.Recipients) != 3 || !ks.RecoveryWrappedCurrent {
		t.Fatalf("key status: %+v %v", ks, err)
	}

	// Job: created by the device, synced into the local store.
	a.reportConsent(t, docs)
	job, err := a.c.CreateJob(ctx, wire.JobCreateRequest{RepoID: rp.ID, Name: "Documents", Kind: wire.JobKindFolder, Enabled: true,
		Spec:     wire.JobSpec{Paths: []string{snapshot.TokenDocuments}, Exclude: []string{"*.tmp"}},
		Schedule: wire.JobSchedule{Type: wire.JobScheduleManual}})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := a.st.SyncFromServer(ctx, a.c.Jobs, a.c.DeviceID); err != nil {
		t.Fatal(err)
	}
	if lj, err := a.st.Job(ctx, job.ID); err != nil || lj.RepoID != rp.ID || !lj.Runnable() {
		t.Fatalf("local job: %+v %v", lj, err)
	}
	a.serve(t, docs)

	// First backup via a backup_now device task.
	run1 := backupNow(t, srv, a, job.ID, 0)
	if run1.Stats.FilesDone != 3 || run1.Stats.BytesNew != 1<<20+2000+5000 {
		t.Fatalf("run 1 stats: %+v", run1.Stats)
	}
	if out := srv.Do("GET", fmt.Sprintf("/api/v1/backup/runs/%d", run1.RunID), nil, 200); out["status"] != wire.RunStatusCompleted {
		t.Fatalf("server run 1: %v", out)
	}
	sn1 := waitBrowsable(t, srv, run1.SnapshotID)
	if sn1["device_id"] != float64(a.c.DeviceID) || sn1["browsable"] != true {
		t.Fatalf("snapshot 1: %v", sn1)
	}
	tree := func(id int64, p string) string {
		return listNames(srv.Do("GET", fmt.Sprintf("/api/v1/snapshots/%d/tree?path=%s", id, url.QueryEscape(p)), nil, 200))
	}
	search := func(id int64, q string) string {
		return listNames(srv.Do("GET", fmt.Sprintf("/api/v1/snapshots/%d/search?q=%s", id, url.QueryEscape(q)), nil, 200))
	}
	if got := tree(run1.SnapshotID, snapshot.TokenDocuments); got != "Projects|Quarterly Report 2024.pdf|notes.txt" {
		t.Fatalf("browse: %s", got)
	}
	if got := tree(run1.SnapshotID, snapshot.TokenDocuments+"/Projects"); got != "vaultdock-design.md" {
		t.Fatalf("browse Projects (exclude *.tmp): %s", got)
	}

	// Incremental: one changed file, one new file; the rest is unchanged
	// and comes from the file cache.
	writeFile(t, filepath.Join(docs, "notes.txt"), 2500, time.Minute)
	writeFile(t, filepath.Join(docs, "Projects", "budget.xlsx"), 64<<10, time.Minute)
	run2 := backupNow(t, srv, a, job.ID, 1)
	if run2.Stats.BytesNew != 2500+64<<10 || run2.SnapshotID == run1.SnapshotID {
		t.Fatalf("run 2 stats: %+v", run2.Stats)
	}
	sn2 := waitBrowsable(t, srv, run2.SnapshotID)
	if sn2["parent_id"] != float64(run1.SnapshotID) {
		t.Fatalf("parent link: %v", sn2)
	}
	if got := search(run2.SnapshotID, "budget"); got != "budget.xlsx" {
		t.Fatalf("search snapshot 2: %q", got)
	}
	if got := search(run1.SnapshotID, "budget"); got != "" {
		t.Fatalf("search snapshot 1: %q", got)
	}

	// Second device: the server shares the keys (full escrow) with a
	// key_share task that B's task loop completes through its keystore.
	b := newAgent(t, srv, "PC-B")
	b.serve(t, t.TempDir())
	waitFor(t, 20*time.Second, "key_share done", func() bool {
		out := srv.Do("GET", fmt.Sprintf("/api/v1/device/tasks/list?device=%d", b.c.DeviceID), nil, 200)
		items, _ := out["items"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			if m["kind"] == wire.TaskKindKeyShare {
				if m["status"] == wire.TaskStatusFailed {
					t.Fatalf("key_share failed: %v", m)
				}
				return m["status"] == wire.TaskStatusDone
			}
		}
		return false
	})
	br, err := b.ks.OpenRepo(ctx, server.New(b.c, rp.ID, server.Options{Logger: quiet}))
	if err != nil {
		t.Fatalf("B open repo: %v", err)
	}
	defer br.Close()
	root, got := restore(t, br, sn2["snapshot_oid"].(string))
	if root.Parent == nil || *root.Parent != sn1["snapshot_oid"] || root.Device.ID != a.c.DeviceID || root.Device.KeyID == "" {
		t.Fatalf("root: parent %v device %+v", root.Parent, root.Device)
	}
	sameFiles(t, got, diskFiles(t, docs, isExcluded))
}

// TestBackupAsSystemSkipped documents the pieces the in-process run above
// replaces with fakes.
func TestBackupAsSystemSkipped(t *testing.T) {
	t.Skip("needs the LocalSystem service: real user tokens (WTSQueryUserToken/S4U), machine DPAPI for box.key, " +
		"the path guard and VSS are not exercised here; TestBackupEngineEndToEnd uses fakes for them")
}
