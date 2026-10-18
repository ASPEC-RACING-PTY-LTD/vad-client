package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineDirsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDataDir, dir)
	if got := MachineDataDir(); got != filepath.Clean(dir) {
		t.Fatalf("MachineDataDir = %q, want %q", got, dir)
	}
	for name, fn := range map[string]func() string{
		"state": StateDir, "keys": KeysDir, "logs": MachineLogDir, "config": ConfigDir, "spool": SpoolDir,
	} {
		if got, want := fn(), filepath.Join(dir, name); got != want {
			t.Errorf("%s dir = %q, want %q", name, got, want)
		}
	}
	if err := EnsureMachineDirs(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{StateDir(), KeysDir(), MachineLogDir(), ConfigDir(), SpoolDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s not created: %v", d, err)
		}
	}
}

func TestMachineDataDirDefault(t *testing.T) {
	t.Setenv(EnvDataDir, "")
	got := MachineDataDir()
	if filepath.Base(got) != "VaultDock" || !filepath.IsAbs(got) {
		t.Fatalf("MachineDataDir = %q", got)
	}
	if strings.Contains(strings.ToLower(got), "appdata") {
		t.Fatalf("machine data dir must not be per-user: %q", got)
	}
}
