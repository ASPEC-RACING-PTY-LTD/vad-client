package agent

import (
	"context"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

func newJob(name string) wire.Job {
	return wire.Job{Name: name, Kind: wire.JobKindFolder, Spec: wire.JobSpec{Paths: []string{"{DOCUMENTS}"}},
		Schedule: wire.JobSchedule{Type: "manual"}, Enabled: true}
}

func TestSaveBackupJobCreateUpdateConflict(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	if _, err := env.api.SaveBackupJob(ctx, newJob("Docs")); code(err) != CodeUnavailable {
		t.Fatalf("save while disconnected err = %v", err)
	}
	env.startVerified(t)
	if _, err := env.api.SaveBackupJob(ctx, newJob("Docs")); code(err) != CodeInvalid {
		t.Fatalf("save without repository err = %v", err)
	}
	env.h.st.Set(kvRepoID, "7")

	for name, j := range map[string]wire.Job{
		"no name":   newJob(" "),
		"bad kind":  func() wire.Job { j := newJob("x"); j.Kind = wire.JobKindRegistry; return j }(),
		"no paths":  func() wire.Job { j := newJob("x"); j.Spec.Paths = nil; return j }(),
		"relative":  func() wire.Job { j := newJob("x"); j.Spec.Paths = []string{`docs\x`}; return j }(),
		"bad token": func() wire.Job { j := newJob("x"); j.Spec.Paths = []string{"{NOPE"}; return j }(),
		"bad mode":  func() wire.Job { j := newJob("x"); j.Mode = "weird"; return j }(),
	} {
		if _, err := env.api.SaveBackupJob(ctx, j); code(err) != CodeInvalid {
			t.Fatalf("%s: err = %v", name, err)
		}
	}

	j := newJob("Docs")
	j.ProfileSID = userSID
	got, err := env.api.SaveBackupJob(ctx, j)
	if err != nil || got.ID != 1 || got.RepoID != 7 || got.Mode != wire.JobModeFull || got.ProfileSID != userSID {
		t.Fatalf("create = %+v, %v", got, err)
	}
	if js, _ := env.api.ListBackupJobs(ctx); len(js) != 1 {
		t.Fatalf("jobs after create = %+v", js)
	}

	got.Name = "Documents"
	got.Revision = 5
	if _, err := env.api.SaveBackupJob(ctx, got.Job); code(err) != CodeConflict {
		t.Fatalf("stale revision err = %v", err)
	}
	got.Revision = 1
	upd, err := env.api.SaveBackupJob(ctx, got.Job)
	if err != nil || upd.Name != "Documents" || upd.Revision != 2 {
		t.Fatalf("update = %+v, %v", upd, err)
	}
}

func TestSaveBackupJobPipeUserForcesProfileAndChecksPaths(t *testing.T) {
	srv, eng := newFakeBackupServer(t), &fakeBackupEngine{}
	dir := t.TempDir()
	o := backupTestOptions(t, dir, srv, eng)
	toks := &fakeTokens{tok: &fakeToken{sid: userSID}}
	o.Tokens = toks
	o.Profiles = fakeProfiles{{SID: userSID, ProfilePath: dir}}
	blocked := false
	o.Guard = func(guard.Op, string, guard.Profile) error {
		if blocked {
			return &guard.BlockedError{Reason: "protected", Err: guard.ErrOutsideProfile}
		}
		return nil
	}
	h, err := NewHost(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	env := &backupEnv{h: h, api: NewInProc(h), srv: srv, eng: eng}
	env.c = newBackupEnv(t).c
	env.startVerified(t)
	h.st.Set(kvRepoID, "7")

	old := folderTokens
	t.Cleanup(func() { folderTokens = old })
	folderTokens = func(context.Context, guard.Profile) (snapshot.Tokens, error) {
		tok, _, _ := snapshot.SplitTokenPath("{DOCUMENTS}")
		return snapshot.Tokens{tok: dir}, nil
	}

	user := caller.With(context.Background(), caller.Caller{SID: userSID})
	j := newJob("Mine")
	j.ProfileSID = "S-1-5-21-9-9-9-500" // ignored for a pipe user
	got, err := h.SaveBackupJob(user, j)
	if err != nil || got.ProfileSID != userSID {
		t.Fatalf("pipe save = %+v, %v", got, err)
	}

	blocked = true
	if _, err := h.SaveBackupJob(user, newJob("Blocked")); code(err) != CodeAccessDenied {
		t.Fatalf("guarded save err = %v", err)
	}
	blocked = false

	// Another profile's job is invisible to this user.
	srv.mu.Lock()
	srv.jobs = append(srv.jobs, testJob(2, "S-1-5-21-9-9-9-500"))
	srv.mu.Unlock()
	h.syncJobs(h.bk.current())
	other := testJob(2, "")
	if _, err := h.SaveBackupJob(user, other); code(err) != CodeNotFound {
		t.Fatalf("save other profile's job err = %v", err)
	}
	if err := h.SkipIssues(user, SkipIssuesRequest{JobID: 2, Classes: []string{wire.IssueClassLocked}}); code(err) != CodeNotFound {
		t.Fatalf("skip other profile's job err = %v", err)
	}
}

func TestSkipIssuesMapsLocalIDs(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	env.startVerified(t, testJob(1, ""))
	if err := env.api.SkipIssues(ctx, SkipIssuesRequest{JobID: 1}); code(err) != CodeInvalid {
		t.Fatalf("empty skip err = %v", err)
	}
	if err := env.api.SkipIssues(ctx, SkipIssuesRequest{JobID: 1, Classes: []string{wire.IssueClassLocked}}); code(err) != CodeNotFound {
		t.Fatalf("skip before any run err = %v", err)
	}
	if _, err := env.api.RunBackup(ctx, 1); err != nil {
		t.Fatal(err)
	}
	runs := env.waitRunDone(t, 1, 1)
	var issues []BackupIssue
	waitFor(t, "issues reported", func() bool {
		issues, _ = env.api.ListIssues(ctx, IssueQuery{JobID: 1})
		return len(issues) > 0 && issues[0].Reported
	})
	if err := env.api.SkipIssues(ctx, SkipIssuesRequest{JobID: 1, IssueIDs: []int64{issues[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if err := env.api.SkipIssues(ctx, SkipIssuesRequest{JobID: 1, Classes: []string{wire.IssueClassLocked}}); err != nil {
		t.Fatal(err)
	}
	if err := env.api.SkipIssues(ctx, SkipIssuesRequest{JobID: 1, IssueIDs: []int64{999999}}); code(err) != CodeNotFound {
		t.Fatalf("unknown issue err = %v", err)
	}
	env.srv.mu.Lock()
	skips := append([]skipCall(nil), env.srv.skips...)
	env.srv.mu.Unlock()
	if len(skips) != 2 || skips[0].runID != runs[0].RunID || len(skips[0].req.IDs) != 1 || skips[0].req.IDs[0] != runs[0].RunID*1000 ||
		skips[1].req.Class != wire.IssueClassLocked {
		t.Fatalf("skips = %+v", skips)
	}
}

func TestGetModeAndGiveConsentPreconditions(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	if _, err := env.api.GetMode(ctx); err != nil && code(err) != CodeUnavailable {
		t.Fatalf("GetMode err = %v", err)
	}
	sys := caller.With(ctx, caller.Caller{SID: sidSystem, System: true})
	if err := env.h.GiveConsent(sys, []byte("x")); code(err) != CodeInvalid {
		t.Fatalf("consent as SYSTEM err = %v", err)
	}
	user := caller.With(ctx, caller.Caller{SID: userSID})
	if err := env.h.GiveConsent(user, []byte("x")); code(err) != CodeUnavailable {
		t.Fatalf("consent while disconnected err = %v", err)
	}
}
