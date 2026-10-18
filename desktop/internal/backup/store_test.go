package backup

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/dbutil"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ms := int64(1_700_000_000_000)
	s.Now = func() time.Time { ms++; return time.UnixMilli(ms) }
	return s
}

func TestStoreMigrations(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SchemaVersion()
	if err != nil || v != len(migrations) || v != 6 {
		t.Fatalf("version = %d, %v; want 6", v, err)
	}
	s.Close()
	// Reopening is a no-op migration.
	s, err = OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(); v != 6 {
		t.Fatalf("reopen version = %d", v)
	}
}

func TestStoreRuns(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	if err := s.PutRun(ctx, Run{}); err == nil {
		t.Fatal("run without uuid accepted")
	}
	r := Run{ClientRunUUID: "u1", JobID: 7, Epoch: 2, Trigger: "manual", Status: wire.RunStatusScanning}
	if err := s.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRun(ctx, Run{ClientRunUUID: "u0", JobID: 7, Status: wire.RunStatusScanning, StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunServerID(ctx, "u1", 99); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunServerID(ctx, "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetRunServerID unknown = %v", err)
	}
	if err := s.UpdateRunStats(ctx, "u1", wire.RunStatusScanning, RunStats{FilesDone: 3}); err != nil {
		t.Fatal(err)
	}
	un, err := s.UnfinishedRuns(ctx)
	if err != nil || len(un) != 2 {
		t.Fatalf("unfinished = %v, %v", un, err)
	}
	if err := s.FinishRun(ctx, "u1", wire.RunStatusCompleted, 42, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != 99 || got.SnapshotID != 42 || got.FinishedAt == 0 || got.Status != wire.RunStatusCompleted ||
		got.Stats.FilesDone != 3 || got.Epoch != 2 || got.Trigger != "manual" {
		t.Fatalf("run = %+v", got)
	}
	if _, err := s.Run(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Run unknown = %v", err)
	}
	runs, err := s.Runs(ctx, 7, 0)
	if err != nil || len(runs) != 2 || runs[0].ClientRunUUID != "u1" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if runs, _ := s.Runs(ctx, 7, 1); len(runs) != 1 {
		t.Fatalf("limit ignored: %d", len(runs))
	}
	if un, _ := s.UnfinishedRuns(ctx); len(un) != 1 || un[0].ClientRunUUID != "u0" {
		t.Fatalf("unfinished after finish = %+v", un)
	}
}

func TestStoreRunSnapshot(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	oid := "ab12"
	if err := s.PutRun(ctx, Run{ClientRunUUID: "u1", JobID: 7, Status: wire.RunStatusCommitting, StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRun(ctx, Run{ClientRunUUID: "u2", JobID: 7, RunID: 55, Status: wire.RunStatusCompleted, StartedAt: 2,
		SnapshotID: 9, SnapshotOID: "cd"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Run(ctx, "u2"); err != nil || got.SnapshotOID != "cd" || got.SnapshotID != 9 {
		t.Fatalf("PutRun snapshot = %+v, %v", got, err)
	}
	if err := s.SetRunServerID(ctx, "u1", 55); err != nil { // same server id: newest wins
		t.Fatal(err)
	}
	if err := s.SetRunSnapshot(ctx, "u1", 42, oid); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunSnapshot(ctx, "nope", 1, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetRunSnapshot unknown = %v", err)
	}
	got, err := s.Run(ctx, "u1")
	if err != nil || got.SnapshotID != 42 || got.SnapshotOID != oid || got.FinishedAt != 0 {
		t.Fatalf("run = %+v, %v", got, err)
	}
	// FinishRun with id 0 keeps the recorded snapshot.
	if err := s.FinishRun(ctx, "u1", wire.RunStatusCompleted, 0, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Run(ctx, "u1"); got.SnapshotID != 42 || got.SnapshotOID != oid || got.FinishedAt == 0 {
		t.Fatalf("finished run = %+v", got)
	}

	tests := []struct {
		runID   int64
		want    string
		wantErr error
	}{
		{55, "u2", nil}, // u2 started later than u1
		{56, "", ErrNotFound},
		{0, "", ErrNotFound},
	}
	for _, tt := range tests {
		r, err := s.RunByServerID(ctx, tt.runID)
		if tt.wantErr != nil {
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("RunByServerID(%d) err = %v", tt.runID, err)
			}
			continue
		}
		if err != nil || r.ClientRunUUID != tt.want {
			t.Errorf("RunByServerID(%d) = %+v, %v", tt.runID, r, err)
		}
	}
}

// TestStoreMigrateFromV5 upgrades a backup.db made before runs.snapshot_oid.
func TestStoreMigrateFromV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := dbutil.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbutil.Migrate(db, migrations[:5], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs (client_run_uuid, job_id, run_id, epoch, status, snapshot_id, started_at, finished_at, updated_at)
		VALUES ('old', 1, 5, 1, 'completed', 8, 1, 2, 2)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.RunByServerID(context.Background(), 5)
	if err != nil || r.ClientRunUUID != "old" || r.SnapshotID != 8 || r.SnapshotOID != "" {
		t.Fatalf("old run = %+v, %v", r, err)
	}
}

func TestStoreFileCache(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	chunks := append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...)
	c := CachedFile{JobID: 1, Path: `C:\a\x.txt`, Size: 10, MTime: 5, CTime: 4, Attrs: AttrArchive, FileID: "id",
		H: []byte{9}, Chunks: chunks, Epoch: 1, SeenRun: "r1"}
	if err := s.PutFileCache(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFileCache(ctx, CachedFile{JobID: 1, Path: `C:\a\old.txt`, Epoch: 1, SeenRun: "r0"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.FileCache(ctx, 1, c.Path)
	if err != nil || !ok || got.Size != 10 || got.Attrs != AttrArchive || got.FileID != "id" || got.SeenRun != "r1" {
		t.Fatalf("cache = %+v, %v, %v", got, ok, err)
	}
	if ids := got.ChunkIDs(); len(ids) != 2 || ids[0][0] != 1 || ids[1][31] != 2 {
		t.Fatalf("chunk ids = %v", ids)
	}
	if _, ok, err := s.FileCache(ctx, 2, c.Path); ok || err != nil {
		t.Fatalf("other job hit: %v %v", ok, err)
	}
	if err := s.MarkFileSeen(ctx, 1, c.Path, "r2"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFileSeen(ctx, 1, `C:\missing`, "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkFileSeen missing = %v", err)
	}
	n, err := s.PruneFileCache(ctx, 1, "r2")
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if _, ok, _ := s.FileCache(ctx, 1, c.Path); !ok {
		t.Fatal("seen entry pruned")
	}
	if err := s.DropFileCache(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.FileCache(ctx, 1, c.Path); ok {
		t.Fatal("entry survived drop")
	}
}

func TestStorePacks(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	if err := s.AddPack(ctx, Pack{}); err == nil {
		t.Fatal("pack without oid accepted")
	}
	for _, p := range []Pack{{OID: "a", RepoID: 1, Size: 10}, {OID: "b", RepoID: 1, Class: 1}, {OID: "c", RepoID: 2}} {
		if err := s.AddPack(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPack(ctx, Pack{OID: "a", RepoID: 1, Size: 99}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Pack(ctx, "a"); err != nil || p.Size != 10 {
		t.Fatalf("re-add changed pack: %+v, %v", p, err)
	}
	if err := s.MarkPackUploaded(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPackUploaded(ctx, "zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark unknown = %v", err)
	}
	pend, err := s.PendingPacks(ctx, 1)
	if err != nil || len(pend) != 1 || pend[0].OID != "b" || pend[0].Class != 1 {
		t.Fatalf("pending = %+v, %v", pend, err)
	}
	p, _ := s.Pack(ctx, "a")
	if !p.Uploaded || p.UploadedAt == 0 {
		t.Fatalf("uploaded pack = %+v", p)
	}
	if _, err := s.Pack(ctx, "zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Pack unknown = %v", err)
	}
}

func TestStoreIssues(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	is := Issue{Path: `C:\a`, Class: wire.IssueClassLocked, Severity: wire.IssueSeveritySkipped, Detail: "d1", Retryable: true}
	for range 3 {
		if err := s.AddIssue(ctx, 1, "r1", is); err != nil {
			t.Fatal(err)
		}
	}
	efs := Issue{Path: `C:\b`, Class: IssueClassEFSNeedsLogon, Severity: wire.IssueSeveritySkipped, Detail: "encrypted"}
	if err := s.AddIssue(ctx, 1, "r1", efs); err != nil {
		t.Fatal(err)
	}
	if err := s.AddIssue(ctx, 1, "r2", is); err != nil {
		t.Fatal(err)
	}
	pend, err := s.PendingIssues(ctx, "r1", 0)
	if err != nil || len(pend) != 2 {
		t.Fatalf("pending = %+v, %v", pend, err)
	}
	if pend[0].Count != 3 || !pend[0].Retryable || pend[0].LastSeen <= pend[0].FirstSeen {
		t.Fatalf("merged issue = %+v", pend[0])
	}
	rep := pend[1].Report()
	if rep.Class != wire.IssueClassAccessDenied || rep.Detail != "efs_needs_logon: encrypted" || rep.Path != `C:\b` {
		t.Fatalf("efs report = %+v", rep)
	}
	if rep := pend[0].Report(); rep.Class != wire.IssueClassLocked || rep.Detail != "d1" || rep.Count != 3 {
		t.Fatalf("locked report = %+v", rep)
	}
	if err := s.MarkIssuesReported(ctx, []int64{pend[0].ID}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PendingIssues(ctx, "r1", 10); len(p) != 1 || p[0].Class != IssueClassEFSNeedsLogon {
		t.Fatalf("after report = %+v", p)
	}
	// A repeat after reporting re-opens the row.
	if err := s.AddIssue(ctx, 1, "r1", is); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PendingIssues(ctx, "r1", 1); len(p) != 1 || p[0].Count != 4 {
		t.Fatalf("reopened = %+v", p)
	}
}

func TestWireClass(t *testing.T) {
	for in, want := range map[string]string{
		IssueClassEFSNeedsLogon:     wire.IssueClassAccessDenied,
		IssueClassProfileNeedsLogon: wire.IssueClassOther,
		wire.IssueClassLocked:       wire.IssueClassLocked,
	} {
		if got := WireClass(in); got != want {
			t.Errorf("WireClass(%q) = %q, want %q", in, got, want)
		}
	}
	p := PendingIssue{Issue: Issue{Class: IssueClassProfileNeedsLogon, Detail: "sign in"}}
	if r := p.Report(); r.Class != wire.IssueClassOther || r.Detail != "profile_needs_logon: sign in" {
		t.Fatalf("report = %+v", r)
	}
}

func TestStoreDeletePack(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	for _, oid := range []string{"a", "b"} {
		if err := s.AddPack(ctx, Pack{OID: oid, RepoID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeletePack(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pack(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted pack still there: %v", err)
	}
	if err := s.DeletePack(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice = %v", err)
	}
	pend, err := s.PendingPacks(ctx, 1)
	if err != nil || len(pend) != 1 || pend[0].OID != "b" {
		t.Fatalf("pending = %+v, %v", pend, err)
	}
	// A deleted pack can be added again (rebuilt with the same oid).
	if err := s.AddPack(ctx, Pack{OID: "a", RepoID: 1, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Pack(ctx, "a"); err != nil || p.Size != 3 || p.Uploaded {
		t.Fatalf("re-added pack %+v, %v", p, err)
	}
}
