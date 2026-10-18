package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
)

func TestPrivilegesForMode(t *testing.T) {
	simple := []string{SeChangeNotifyPrivilege, SeImpersonatePrivilege, SeTcbPrivilege, SeCreateGlobalPrivilege}
	advanced := append(append([]string{}, simple...), SeBackupPrivilege, SeManageVolumePrivilege)
	tests := []struct {
		name  string
		mode  modules.Mode
		extra []string
		want  []string
		err   bool
	}{
		{"simple", modules.ModeSimple, nil, simple, false},
		{"advanced", modules.ModeAdvanced, nil, advanced, false},
		{"extras deduped", modules.ModeSimple,
			[]string{SeTcbPrivilege, SeAssignPrimaryTokenPrivilege, SeAssignPrimaryTokenPrivilege},
			append(append([]string{}, simple...), SeAssignPrimaryTokenPrivilege), false},
		{"restore only as explicit extra", modules.ModeAdvanced, []string{SeRestorePrivilege},
			append(append([]string{}, advanced...), SeRestorePrivilege), false},
		{"unknown mode", modules.Mode("expert"), nil, nil, true},
		{"forbidden privilege", modules.ModeSimple, []string{"SeDebugPrivilege"}, nil, true},
		{"case sensitive", modules.ModeSimple, []string{"setcbprivilege"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PrivilegesForMode(tt.mode, tt.extra...)
			if (err != nil) != tt.err {
				t.Fatalf("err = %v, want err %v", err, tt.err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultSetsDoNotAlias(t *testing.T) {
	a, _ := PrivilegesForMode(modules.ModeAdvanced)
	a[1] = "x"
	if SimplePrivileges[1] != SeImpersonatePrivilege {
		t.Fatal("PrivilegesForMode aliased SimplePrivileges")
	}
	if slices.Contains(AdvancedExtraPrivileges, SeRestorePrivilege) {
		t.Fatal("SeRestore must not be in the default Advanced set (MASTER_PLAN section 6)")
	}
}

func TestNormalizeAlwaysHasChangeNotify(t *testing.T) {
	got, err := NormalizePrivileges([]string{SeBackupPrivilege})
	if err != nil || !slices.Equal(got, []string{SeChangeNotifyPrivilege, SeBackupPrivilege}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSessionEventTypeString(t *testing.T) {
	if SessionLogon.String() != "logon" || SessionUnlock.String() != "unlock" {
		t.Fatal("names")
	}
	if got := SessionEventType(99).String(); got != "session_event_99" {
		t.Fatal(got)
	}
}

type fakeHandler struct {
	startErr  error
	stopDelay time.Duration
	started   atomic.Bool
	stopped   atomic.Bool
	startCtx  context.Context
	exit      chan struct{}
}

func (f *fakeHandler) Start(ctx context.Context) error {
	f.startCtx = ctx
	if f.startErr != nil {
		return f.startErr
	}
	f.started.Store(true)
	return nil
}

func (f *fakeHandler) Stop(ctx context.Context) error {
	select {
	case <-time.After(f.stopDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	f.stopped.Store(true)
	return nil
}

func (f *fakeHandler) SessionChange(SessionEvent) {}

func (f *fakeHandler) ExitRequested() <-chan struct{} { return f.exit }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunUntilContextCancel(t *testing.T) {
	f := &fakeHandler{exit: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- runUntil(ctx, f, Options{Log: quiet()}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !f.started.Load() || !f.stopped.Load() {
		t.Fatal("not started/stopped")
	}
	if f.startCtx.Err() == nil {
		t.Fatal("start ctx must be cancelled after stop")
	}
}

func TestRunUntilExitRequest(t *testing.T) {
	f := &fakeHandler{exit: make(chan struct{})}
	close(f.exit)
	if err := runUntil(context.Background(), f, Options{Log: quiet()}); err != nil || !f.stopped.Load() {
		t.Fatalf("err %v stopped %v", err, f.stopped.Load())
	}
}

func TestRunUntilStartError(t *testing.T) {
	boom := errors.New("boom")
	f := &fakeHandler{startErr: boom}
	if err := runUntil(context.Background(), f, Options{Log: quiet()}); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if f.stopped.Load() {
		t.Fatal("stop must not run after a failed start")
	}
}

func TestRunUntilStopTimeout(t *testing.T) {
	f := &fakeHandler{exit: make(chan struct{}), stopDelay: time.Hour}
	close(f.exit)
	err := runUntil(context.Background(), f, Options{Log: quiet(), StopTimeout: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenLogRotates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	log, c, err := OpenLog(dir, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	rf := c.(*rotatingFile)
	rf.max = 200
	for i := 0; i < 20; i++ {
		log.Info("line", "i", i)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LogFileName+".1")); err != nil {
		t.Fatal("expected rotation:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LogFileName+".4")); err == nil {
		t.Fatal("kept too many files")
	}
	if _, err := rf.Write([]byte("x")); err == nil {
		t.Fatal("write after close must fail")
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.Name != DefaultName || c.DisplayName == "" || c.Description == "" || c.PreshutdownTimeout != time.Minute {
		t.Fatalf("%+v", c)
	}
	if !strings.HasSuffix(DefaultLogDir(), filepath.Join("VaultDock", "logs")) {
		t.Fatal(DefaultLogDir())
	}
}
