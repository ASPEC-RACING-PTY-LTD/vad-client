package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
)

func testEngine(t *testing.T, dbPath string) (*Engine, *state.Store) {
	t.Helper()
	st, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "test"), st
}

func TestScheduleLastSurvivesRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	every := 10 * time.Minute
	t0 := time.Now().Truncate(time.Millisecond)
	key := "7/n1"

	e, st := testEngine(t, db)
	if e.scheduleDue(key, t0, every) {
		t.Fatal("first sighting must not fire")
	}
	if e.scheduleDue(key, t0.Add(5*time.Minute), every) {
		t.Fatal("fired before the interval elapsed")
	}
	st.Close()

	// Restart: the interval started at t0 continues instead of restarting.
	e, _ = testEngine(t, db)
	if e.scheduleDue(key, t0.Add(9*time.Minute), every) {
		t.Fatal("re-fired after restart before the interval elapsed")
	}
	if !e.scheduleDue(key, t0.Add(10*time.Minute), every) {
		t.Fatal("due run skipped after restart")
	}
	if e.scheduleDue(key, t0.Add(15*time.Minute), every) {
		t.Fatal("fired twice in one interval")
	}
	if !e.scheduleDue(key, t0.Add(20*time.Minute), every) {
		t.Fatal("second interval did not fire")
	}
}

func TestScheduleLastClockMovedBack(t *testing.T) {
	e, st := testEngine(t, filepath.Join(t.TempDir(), "state.db"))
	t0 := time.Now().Truncate(time.Millisecond)
	if err := st.SetScheduleLast("1/n", t0.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if e.scheduleDue("1/n", t0, 10*time.Minute) {
		t.Fatal("must not fire when the stored time is in the future")
	}
	if last, ok, _ := st.ScheduleLast("1/n"); !ok || last != t0.UnixMilli() {
		t.Fatalf("interval not restarted from now: %d %v", last, ok)
	}
	if !e.scheduleDue("1/n", t0.Add(10*time.Minute), 10*time.Minute) {
		t.Fatal("did not fire after the restarted interval")
	}
}

func TestInvalidAutomationRecordedAsIssue(t *testing.T) {
	e, st := testEngine(t, filepath.Join(t.TempDir(), "state.db"))
	tpl := automation.BuiltinTemplates()[0].Automation
	tpl.Enabled = true
	valid, err := json.Marshal(tpl)
	if err != nil {
		t.Fatal(err)
	}
	list := []client.RemoteAutomation{
		{ID: 1, Name: "good", Definition: valid},
		{ID: 2, Name: "broken", Definition: json.RawMessage(`{"schema":"vaultdock.automation/v1","name":"x","nodes":[],"edges":[],"bogus":1}`)},
		{ID: 3, Name: "garbage", Definition: json.RawMessage(`"nope"`)},
	}
	for i := 0; i < 2; i++ {
		named := e.activeAutomations(list)
		if len(named) != 1 || named[0].ID != "1" {
			t.Fatalf("active automations = %+v", named)
		}
	}
	rows, err := st.DB.Query(`SELECT rel_path, class, count, state, message FROM issues WHERE source = 'automation' ORDER BY rel_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var rel, class, st, msg string
		var count int
		if err := rows.Scan(&rel, &class, &count, &st, &msg); err != nil {
			t.Fatal(err)
		}
		if class != issueInvalid || count != 2 || st != "open" || msg == "" {
			t.Errorf("issue %s: class=%s count=%d state=%s msg=%q", rel, class, count, st, msg)
		}
		got = append(got, rel)
	}
	if len(got) != 2 || got[0] != "2" || got[1] != "3" {
		t.Fatalf("issues for %v, want [2 3]", got)
	}
}
