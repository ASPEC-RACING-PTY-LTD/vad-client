package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

func wj(id, rev int64, name string, mods ...string) wire.Job {
	return wire.Job{ID: id, DeviceID: 1, Revision: rev, Name: name, Enabled: true, RequiredModules: mods,
		ProfileSID: "S-1-5-21-1-2-3-1001", Spec: wire.JobSpec{Paths: []string{"{DOCUMENTS}"}}}
}

func TestJobModel(t *testing.T) {
	j := Job{Job: wj(1, 1, "a", "backup")}
	if !j.Runnable() || j.EffectiveState() != wire.JobStateActive {
		t.Fatalf("default job not runnable: %s", j.EffectiveState())
	}
	j.SuspendedByMode = true
	if j.Runnable() || j.EffectiveState() != wire.JobStateSuspendedByMode {
		t.Fatal("suspended job runnable")
	}
	j = Job{Job: wj(1, 1, "a")}
	j.State = wire.JobStateSuspendedByProfile
	if j.Runnable() {
		t.Fatal("profile-suspended job runnable")
	}
	j.State, j.Enabled = "", false
	if j.Runnable() {
		t.Fatal("disabled job runnable")
	}
	if r := (&Job{Job: wj(1, 1, "a", "x", "y")}).Requires(); !slices.Equal(r, []sdkmod.ID{"x", "y"}) {
		t.Fatalf("requires = %v", r)
	}
}

func TestJobAccessors(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	if err := s.PutJob(ctx, Job{}); err == nil {
		t.Fatal("job without id accepted")
	}
	if err := s.PutJob(ctx, Job{Job: wj(2, 1, "b")}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutJob(ctx, Job{Job: wj(1, 1, "a")}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.Jobs(ctx)
	if err != nil || len(jobs) != 2 || jobs[0].ID != 1 || jobs[1].Name != "b" || jobs[0].SyncedAt == 0 {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
	if err := s.SetJobSuspended(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobSuspended(ctx, 9, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspend unknown = %v", err)
	}
	j, err := s.Job(ctx, 1)
	if err != nil || !j.SuspendedByMode || j.Spec.Paths[0] != "{DOCUMENTS}" {
		t.Fatalf("job = %+v, %v", j, err)
	}
	if err := s.PutFileCache(ctx, CachedFile{JobID: 1, Path: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Job(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted job = %v", err)
	}
	if _, ok, _ := s.FileCache(ctx, 1, "p"); ok {
		t.Fatal("file cache survived job delete")
	}
}

func TestSyncJobs(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	res, err := s.SyncJobs(ctx, []wire.Job{wj(1, 1, "a"), wj(2, 3, "b"), wj(3, 1, "c")}, 1, true)
	if err != nil || len(res.Added) != 3 {
		t.Fatalf("first sync = %+v, %v", res, err)
	}
	if err := s.SetJobSuspended(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFileCache(ctx, CachedFile{JobID: 3, Path: "p"}); err != nil {
		t.Fatal(err)
	}
	other := wj(4, 1, "other device")
	other.DeviceID = 2
	res, err = s.SyncJobs(ctx, []wire.Job{wj(1, 2, "a2"), wj(2, 2, "b-old"), other, wj(5, 1, "e"), {ID: 0}}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	want := SyncResult{Added: []int64{5}, Updated: []int64{1}, Removed: []int64{3}, Stale: []int64{2}}
	if fmt.Sprint(res) != fmt.Sprint(want) {
		t.Fatalf("sync = %+v, want %+v", res, want)
	}
	j1, _ := s.Job(ctx, 1)
	if j1.Name != "a2" || j1.Revision != 2 || !j1.SuspendedByMode {
		t.Fatalf("updated job = %+v", j1)
	}
	if j2, _ := s.Job(ctx, 2); j2.Name != "b" || j2.Revision != 3 {
		t.Fatalf("stale replaced job: %+v", j2)
	}
	if _, err := s.Job(ctx, 4); !errors.Is(err, ErrNotFound) {
		t.Fatal("other device's job mirrored")
	}
	if _, ok, _ := s.FileCache(ctx, 3, "p"); ok {
		t.Fatal("removed job's cache kept")
	}
	// Equal revision is a no-op; an incomplete list removes nothing.
	res, err = s.SyncJobs(ctx, []wire.Job{wj(1, 2, "ignored")}, 1, false)
	if err != nil || len(res.Added)+len(res.Updated)+len(res.Removed)+len(res.Stale) != 0 {
		t.Fatalf("no-op sync = %+v, %v", res, err)
	}
	if j1, _ := s.Job(ctx, 1); j1.Name != "a2" {
		t.Fatal("equal revision replaced the job")
	}
	if jobs, _ := s.Jobs(ctx); len(jobs) != 3 {
		t.Fatalf("partial sync removed jobs: %d left", len(jobs))
	}
}

func TestSyncFromServer(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	pages := map[string]*wire.List[wire.Job]{
		"":   {Items: []wire.Job{wj(1, 1, "a")}, NextCursor: "c1"},
		"c1": {Items: []wire.Job{wj(2, 1, "b")}},
	}
	list := func(_ context.Context, cursor string, _ int) (*wire.List[wire.Job], error) { return pages[cursor], nil }
	res, err := s.SyncFromServer(ctx, list, 1)
	if err != nil || !slices.Equal(res.Added, []int64{1, 2}) {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	boom := errors.New("boom")
	if _, err := s.SyncFromServer(ctx, func(context.Context, string, int) (*wire.List[wire.Job], error) { return nil, boom }, 1); !errors.Is(err, boom) {
		t.Fatalf("list error = %v", err)
	}
	if jobs, _ := s.Jobs(ctx); len(jobs) != 2 {
		t.Fatal("failed fetch changed the mirror")
	}
}

func TestJobSource(t *testing.T) {
	s, ctx := openTestStore(t), context.Background()
	var _ modules.ItemSource = NewJobSource(s)
	for _, j := range []wire.Job{wj(1, 1, "a", "backup", "vss"), wj(2, 1, "b", "backup"), wj(3, 1, "c")} {
		if err := s.PutJob(ctx, Job{Job: j}); err != nil {
			t.Fatal(err)
		}
	}
	src := NewJobSource(s)
	aff, err := src.Affected(ctx, []sdkmod.ID{"vss"})
	if err != nil || len(aff) != 1 || aff[0].ID != "1" || aff[0].Kind != ItemKindBackupJob {
		t.Fatalf("affected = %+v, %v", aff, err)
	}
	aff = append(aff, modules.AffectedItem{Kind: "sync_folder", ID: "2"}, modules.AffectedItem{Kind: ItemKindBackupJob, ID: "99"})
	if err := src.Suspend(ctx, aff); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Job(ctx, 1); !j.SuspendedByMode {
		t.Fatal("job 1 not suspended")
	}
	if j, _ := s.Job(ctx, 2); j.SuspendedByMode {
		t.Fatal("other kind's id suspended a backup job")
	}
	if aff, _ := src.Affected(ctx, []sdkmod.ID{"vss"}); len(aff) != 0 {
		t.Fatalf("suspended job still affected: %+v", aff)
	}
	if err := src.Resume(ctx, []sdkmod.ID{"backup"}); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Job(ctx, 1); !j.SuspendedByMode {
		t.Fatal("resumed with a module still disabled")
	}
	if err := src.Resume(ctx, []sdkmod.ID{"backup", "vss"}); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Job(ctx, 1); j.SuspendedByMode {
		t.Fatal("job 1 not resumed")
	}
	if jobs, _ := s.Jobs(ctx); len(jobs) != 3 {
		t.Fatal("suspend/resume deleted a job")
	}
}
