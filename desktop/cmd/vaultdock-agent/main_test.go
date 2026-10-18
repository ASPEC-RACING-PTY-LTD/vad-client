package main

import (
	"io"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/service"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
)

func TestParseArgs(t *testing.T) {
	const m = time.Minute
	tests := []struct {
		args []string
		want cmdLine
		err  bool
	}{
		{args: []string{"run"}, want: cmdLine{cmd: "run", timeout: m}},
		{args: []string{"service"}, want: cmdLine{cmd: "run", timeout: m}},
		{args: []string{"run", "-data-dir", `D:\d`, "-log-dir", `D:\l`, "-auto-updates"},
			want: cmdLine{cmd: "run", dataDir: `D:\d`, logDir: `D:\l`, autoUpdates: true, timeout: m}},
		{args: []string{"install"}, want: cmdLine{cmd: "install", mode: modules.ModeSimple, timeout: m}},
		{args: []string{"install", "-mode", "advanced", "-extra-privileges", "SeAssignPrimaryTokenPrivilege, SeIncreaseQuotaPrivilege", "-exe", `C:\a.exe`},
			want: cmdLine{cmd: "install", mode: modules.ModeAdvanced, exe: `C:\a.exe`, timeout: m,
				extraPrivs: []string{"SeAssignPrimaryTokenPrivilege", "SeIncreaseQuotaPrivilege"}}},
		{args: []string{"configure", "-mode", "advanced"}, want: cmdLine{cmd: "configure", mode: modules.ModeAdvanced, timeout: m}},
		{args: []string{"stop", "-timeout", "5s"}, want: cmdLine{cmd: "stop", timeout: 5 * time.Second}},
		{args: []string{"uninstall"}, want: cmdLine{cmd: "uninstall", timeout: m}},
		{args: []string{"start"}, want: cmdLine{cmd: "start", timeout: m}},
		{args: []string{"status"}, want: cmdLine{cmd: "status", timeout: m}},
		{args: []string{"help"}, want: cmdLine{cmd: "help"}},
		{args: nil, err: true},
		{args: []string{"frobnicate"}, err: true},
		{args: []string{"install", "-mode", "expert"}, err: true},
		{args: []string{"install", "-extra-privileges", "SeDebugPrivilege"}, err: true},
		{args: []string{"status", "extra"}, err: true},
		{args: []string{"start", "-timeout", "1s"}, err: true},
		{args: []string{"run", "-bogus"}, err: true},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args, io.Discard)
		if (err != nil) != tt.err {
			t.Fatalf("%v: err = %v, want err %v", tt.args, err, tt.err)
		}
		if tt.err {
			continue
		}
		if got.cmd != tt.want.cmd || got.dataDir != tt.want.dataDir || got.logDir != tt.want.logDir ||
			got.mode != tt.want.mode || got.exe != tt.want.exe || got.timeout != tt.want.timeout ||
			got.autoUpdates != tt.want.autoUpdates || !slices.Equal(got.extraPrivs, tt.want.extraPrivs) {
			t.Fatalf("%v: got %+v, want %+v", tt.args, got, tt.want)
		}
	}
}

func TestAdminCommands(t *testing.T) {
	for _, c := range []string{"install", "configure", "uninstall", "start", "stop"} {
		if !adminCommands[c] {
			t.Fatal(c)
		}
	}
	if adminCommands["status"] || adminCommands["run"] {
		t.Fatal("status/run must not require elevation")
	}
}

func TestServiceConfig(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "vaultdock-agent.exe")
	c, err := serviceConfig(cmdLine{cmd: "install", mode: modules.ModeAdvanced, exe: exe})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := service.PrivilegesForMode(modules.ModeAdvanced)
	if c.ExePath != exe || !slices.Equal(c.Args, []string{"service"}) || !slices.Equal(c.Privileges, want) {
		t.Fatalf("%+v", c)
	}
}

func TestRealMainUsageErrors(t *testing.T) {
	if code := realMain([]string{"nope"}); code != 2 {
		t.Fatalf("code %d", code)
	}
	if code := realMain([]string{"help"}); code != 0 {
		t.Fatalf("code %d", code)
	}
}

func TestAgentOptionsServiceFlag(t *testing.T) {
	cl := cmdLine{cmd: "run", dataDir: `D:\d`, autoUpdates: true}
	for _, svc := range []bool{true, false} {
		o := agentOptions(cl, nil, svc)
		if o.Service != svc || o.DataDir != `D:\d` || !o.AutoUpdates || o.UpdateRepo != updateRepo || (o.Backup != nil) != svc {
			t.Fatalf("agentOptions(service=%v) = %+v", svc, o)
		}
		if h := newAgentHandler(nil, o); h.opts.Service != svc || h.opts.RequestExit == nil {
			t.Fatalf("handler options lost the service flag or exit hook")
		}
	}
}
