package agent

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
)

const otherUserSID = "S-1-5-21-1111111111-2222222222-3333333333-1002"

func TestConfigsScopedToOwner(t *testing.T) {
	e := newPathEnv(t)
	bg := context.Background()
	mine, err := e.h.SaveConfig(userCtx(), func() config.SyncConfig {
		c := e.cfg()
		c.OwnerSID = otherUserSID // a pipe client cannot choose the owner
		return c
	}())
	if err != nil {
		t.Fatal(err)
	}
	if mine.OwnerSID != testUserSID {
		t.Fatalf("owner = %q, want the caller", mine.OwnerSID)
	}
	theirs := e.cfg()
	theirs.Name, theirs.OwnerSID = "Theirs", otherUserSID
	theirs, err = e.h.SaveConfig(bg, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if theirs.OwnerSID != otherUserSID {
		t.Fatalf("trusted caller's owner not kept: %q", theirs.OwnerSID)
	}

	// Names are unique per owner only.
	same := e.cfg()
	same.OwnerSID = otherUserSID
	if _, err := e.h.SaveConfig(bg, same); err != nil {
		t.Fatalf("same name for another owner = %v", err)
	}
	if _, err := e.h.SaveConfig(userCtx(), e.cfg()); code(err) != CodeInvalid {
		t.Fatalf("duplicate name for the same owner = %v", err)
	}
	if v, _ := e.h.ListConfigs(userCtx()); len(v) != 1 || v[0].ID != mine.ID {
		t.Fatalf("user sees %+v", v)
	}
	if v, _ := e.h.ListConfigs(bg); len(v) != 3 {
		t.Fatalf("in-process caller sees %d configs", len(v))
	}
	edit := theirs
	edit.Name = "Hijack"
	if _, err := e.h.SaveConfig(userCtx(), edit); code(err) != CodeNotFound {
		t.Fatalf("editing another user's config = %v", err)
	}
	if err := e.h.DeleteConfig(userCtx(), theirs.ID); code(err) != CodeNotFound {
		t.Fatalf("deleting another user's config = %v", err)
	}
	if _, err := e.h.PreviewConfig(userCtx(), theirs); code(err) != CodeNotFound {
		t.Fatalf("previewing another user's config = %v", err)
	}
	// An in-process update without an owner keeps the stored one.
	keep := theirs
	keep.OwnerSID = ""
	if got, err := e.h.SaveConfig(bg, keep); err != nil || got.OwnerSID != otherUserSID {
		t.Fatalf("owner after update = %q, %v", got.OwnerSID, err)
	}

	// Queue, history and retry only cover the caller's configurations.
	for _, id := range []string{mine.ID, theirs.ID} {
		if err := e.h.st.Enqueue(state.Item{ConfigID: id, RelPath: "f.txt", Event: "file.created", RemoteDir: "/", RemoteName: "f.txt", NextAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		e.h.st.AddHistory(state.HistoryEntry{ConfigID: id, RelPath: "f.txt", Status: "completed"})
	}
	if _, err := e.h.st.DB.Exec("UPDATE queue SET status = 'failed'"); err != nil {
		t.Fatal(err)
	}
	if q, _ := e.h.ListQueue(userCtx(), ""); len(q) != 1 || q[0].ConfigID != mine.ID {
		t.Fatalf("user queue = %+v", q)
	}
	if hist, _ := e.h.ListHistory(userCtx(), ""); len(hist) != 1 || hist[0].ConfigID != mine.ID {
		t.Fatalf("user history = %+v", hist)
	}
	if q, _ := e.h.ListQueue(bg, ""); len(q) != 2 {
		t.Fatalf("in-process queue = %d items", len(q))
	}
	if n, err := e.h.RetryFailed(userCtx(), 0); err != nil || n != 1 {
		t.Fatalf("RetryFailed as user = %d, %v", n, err)
	}
}

func TestAssignOwnersAtServiceStart(t *testing.T) {
	dir := t.TempDir()
	profileRoot := filepath.Join(t.TempDir(), "alice")
	docs := filepath.Join(profileRoot, "Documents")
	shared := t.TempDir()
	for _, d := range []string{docs, shared} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []config.SyncConfig{
		{ID: "in-profile", Name: "Docs", Enabled: true, Kind: "folder", LocalPath: docs, Destination: "/Docs", Mode: "manual"},
		{ID: "elsewhere", Name: "Shared", Enabled: true, Kind: "folder", LocalPath: shared, Destination: "/Shared", Mode: "manual"},
	} {
		c.Normalize()
		if err := st.SaveConfig(c); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	opts := testOptions(dir)
	opts.Service = true
	opts.Tokens = &fakeTokens{tok: &fakeToken{sid: testUserSID}}
	opts.Profiles = fakeProfiles{{SID: testUserSID, ProfilePath: profileRoot}}
	h, err := NewHost(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if !h.Engine().EnforcesOwner() {
		t.Fatal("service mode does not enforce owner identity")
	}
	owners := map[string]string{}
	for _, c := range h.Engine().Configs() {
		owners[c.ID] = c.OwnerSID
	}
	if owners["in-profile"] != testUserSID || owners["elsewhere"] != "" {
		t.Fatalf("owners = %v", owners)
	}
	// The unowned folder is not synchronised.
	if _, err := h.Engine().Scan("elsewhere", true); err == nil {
		t.Fatal("unowned folder was scanned")
	}
}

func TestPerUserHostRecordsCurrentUserAsOwner(t *testing.T) {
	e := newPathEnv(t)
	got, err := e.h.SaveConfig(context.Background(), e.cfg())
	if err != nil {
		t.Fatal(err)
	}
	if e.h.Engine().EnforcesOwner() {
		t.Fatal("per-user host enforces owner identity")
	}
	// The process's own SID when it is a user account, otherwise none.
	want := ""
	if u, err := user.Current(); err == nil && tokens.IsUserSID(u.Uid) {
		want = u.Uid
	}
	if got.OwnerSID != want {
		t.Fatalf("owner = %q, want %q", got.OwnerSID, want)
	}
}

func TestEventsScopedToOwner(t *testing.T) {
	e := newPathEnv(t)
	mine, err := e.h.SaveConfig(userCtx(), e.cfg())
	if err != nil {
		t.Fatal(err)
	}
	theirs := e.cfg()
	theirs.Name, theirs.OwnerSID = "Theirs", otherUserSID
	if theirs, err = e.h.SaveConfig(context.Background(), theirs); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(userCtx())
	defer cancel()
	sub, err := e.h.Subscribe(ctx, KindLog, KindIssue, KindNotification)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	adminCtx := caller.With(context.Background(), caller.Caller{SID: testUserSID, Admin: true, Elevated: true})
	adminSub, err := e.h.Subscribe(adminCtx, KindLog)
	if err != nil {
		t.Fatal(err)
	}
	defer adminSub.Close()

	_ = e.h.bus.Publish(KindLog, LogEvent{Message: "scan: skipped entry"})
	_ = e.h.bus.Publish(KindNotification, Notification{Title: "their file", OwnerSID: otherUserSID})
	_ = e.h.bus.Publish(KindIssue, IssueEvent{Source: "scan", ConfigID: theirs.ID, Class: "access_denied"})
	_ = e.h.bus.Publish(KindIssue, IssueEvent{Source: "scan", ConfigID: mine.ID, Class: "access_denied"})

	select {
	case ev := <-sub.C():
		var ie IssueEvent
		if ev.Kind != KindIssue || ev.Decode(&ie) != nil || ie.ConfigID != mine.ID {
			t.Fatalf("first event for the user = %s %s", ev.Kind, ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("own issue event not delivered")
	}
	select {
	case ev := <-sub.C():
		t.Fatalf("unexpected event %s %s", ev.Kind, ev.Data)
	case <-time.After(100 * time.Millisecond):
	}
	_ = e.h.bus.Publish(KindNotification, Notification{Title: "mine", OwnerSID: testUserSID})
	_ = e.h.bus.Publish(KindNotification, Notification{Title: "everyone"})
	for _, want := range []string{"mine", "everyone"} {
		select {
		case ev := <-sub.C():
			var n Notification
			if ev.Kind != KindNotification || ev.Decode(&n) != nil || n.Title != want {
				t.Fatalf("got %s %s, want notification %q", ev.Kind, ev.Data, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("notification %q not delivered", want)
		}
	}
	select {
	case ev := <-adminSub.C():
		if ev.Kind != KindLog {
			t.Fatalf("admin got %s", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("administrator did not get the log event")
	}

	snap := engine.Snapshot{Active: []engine.ActiveView{{ID: 1, ConfigID: mine.ID}, {ID: 2, ConfigID: theirs.ID}}}
	if got := e.h.filterSnapshot(e.h.scopeOf(userCtx()), snap); len(got.Active) != 1 || got.Active[0].ID != 1 {
		t.Fatalf("filtered snapshot = %+v", got.Active)
	}
	if got := e.h.filterSnapshot(e.h.scopeOf(context.Background()), snap); len(got.Active) != 2 {
		t.Fatalf("in-process snapshot = %+v", got.Active)
	}
}
