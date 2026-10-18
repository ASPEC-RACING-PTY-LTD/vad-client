package restore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

type fakeServer struct {
	mu      sync.Mutex
	dr      wire.DeviceRestore
	reports []wire.RestoreProgress
	cancel  bool
}

func (s *fakeServer) DeviceRestore(_ context.Context, id int64) (*wire.DeviceRestore, error) {
	if id != s.dr.Restore.ID {
		return nil, errors.New("not found")
	}
	d := s.dr
	return &d, nil
}

func (s *fakeServer) ReportRestore(_ context.Context, _ int64, p wire.RestoreProgress) (*wire.RestoreProgressReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, p)
	return &wire.RestoreProgressReply{Status: p.Status, CancelRequested: s.cancel}, nil
}

func taskFixture(t *testing.T) (*backupFixture, *fakeServer, *Jobs, string) {
	f := newBackup(t, map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo", "sub/c.txt": "charlie"})
	target := filepath.Join(t.TempDir(), "Documents")
	srv := &fakeServer{dr: wire.DeviceRestore{Restore: wire.Restore{ID: 7, Status: wire.RestoreStatusQueued}, RepoID: 1,
		SnapshotOID: "", TargetProfileSID: testSID, ConflictPolicy: "rename"}}
	j := &Jobs{Server: srv, Guard: allow, ReportItems: 2,
		OpenRepo: func(context.Context, int64) (Repo, error) { return repoNoClose{f.r}, nil },
		Target: func(_ context.Context, sid string) (Target, error) {
			if sid != testSID {
				return Target{}, errors.New("not consented")
			}
			return Target{Profile: guard.Profile{SID: sid, Root: filepath.Dir(target)}, Tokens: snapshot.Tokens{snapshot.TokenDocuments: target}}, nil
		}}
	return f, srv, j, target
}

// repoNoClose keeps the fixture's repository open across restores.
type repoNoClose struct{ *repo.Repo }

func (repoNoClose) Close() {}

func snapshotOID(t *testing.T, f *backupFixture) string { t.Helper(); return f.oid }

func TestTaskRunsAndReports(t *testing.T) {
	f, srv, j, target := taskFixture(t)
	srv.dr.SnapshotOID = snapshotOID(t, f)
	j.OpenRepo = func(context.Context, int64) (Repo, error) { return repoNoClose{f.r}, nil }
	if err := j.Start(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	j.Wait()
	if got := readTree(t, target); got["sub/c.txt"] != "charlie" || len(got) != 3 {
		t.Fatalf("restored %v", got)
	}
	first, last := srv.reports[0], srv.reports[len(srv.reports)-1]
	if first.Status != wire.RestoreStatusRunning || last.Status != wire.RestoreStatusCompleted || last.FilesDone != 3 {
		t.Fatalf("reports first %+v last %+v", first, last)
	}
	items := 0
	for _, r := range srv.reports {
		if len(r.Items) > wire.RestoreProgressItemsMax {
			t.Fatal("report over the item limit")
		}
		for _, it := range r.Items {
			if it.Status == wire.RestoreItemDone {
				items++
			}
		}
	}
	if items != 3 {
		t.Fatalf("done items reported = %d", items)
	}
	// Not runnable any more.
	srv.dr.Restore.Status = wire.RestoreStatusCompleted
	if err := j.Start(context.Background(), 7); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("finished restore started again: %v", err)
	}
}

func TestTaskRefusesUnconsentedProfile(t *testing.T) {
	f, srv, j, _ := taskFixture(t)
	srv.dr.SnapshotOID = snapshotOID(t, f)
	srv.dr.TargetProfileSID = "S-1-5-21-9-9-9-1002"
	if err := j.Start(context.Background(), 7); err == nil {
		t.Fatal("restore into an unconsented profile started")
	}
	if len(srv.reports) != 1 || srv.reports[0].Status != wire.RestoreStatusFailed {
		t.Fatalf("reports %+v", srv.reports)
	}
}

func TestTaskServerCancel(t *testing.T) {
	f, srv, j, _ := taskFixture(t)
	srv.dr.SnapshotOID = snapshotOID(t, f)
	srv.cancel = true
	j.OpenRepo = func(context.Context, int64) (Repo, error) { return repoNoClose{f.r}, nil }
	if err := j.Start(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	j.Wait()
	if last := srv.reports[len(srv.reports)-1]; last.Status != wire.RestoreStatusCancelled {
		t.Fatalf("last report %+v", last)
	}
}
