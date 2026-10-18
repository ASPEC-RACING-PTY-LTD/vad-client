//go:build windows

package service

import (
	"slices"
	"testing"
)

func TestMultiSZRoundTrip(t *testing.T) {
	in := []string{SeChangeNotifyPrivilege, SeTcbPrivilege}
	ms, err := multiSZ(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := parseMultiSZ(&ms[0]); !slices.Equal(got, in) {
		t.Fatalf("got %v", got)
	}
	empty, _ := multiSZ(nil)
	if got := parseMultiSZ(&empty[0]); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestBinaryPathQuoted(t *testing.T) {
	c := Config{ExePath: `C:\Program Files\VaultDock\vaultdock-agent.exe`, Args: []string{"service"}}
	if got := c.binaryPath(); got != `"C:\Program Files\VaultDock\vaultdock-agent.exe" service` {
		t.Fatal(got)
	}
}

func TestConfigValidate(t *testing.T) {
	ok := Config{ExePath: `C:\x\a.exe`, Privileges: SimplePrivileges}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{
		{ExePath: `a.exe`, Privileges: SimplePrivileges},
		{ExePath: `C:\x\a.exe`},
		{ExePath: `C:\x\a.exe`, Privileges: []string{"SeDebugPrivilege"}},
	} {
		if c.validate() == nil {
			t.Fatalf("%+v should be invalid", c)
		}
	}
}

// Query opens SCM with query-only rights, so this runs unelevated.
func TestQueryMissingService(t *testing.T) {
	st, err := Query("VaultDockAgent-test-does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed {
		t.Fatal("must not be installed")
	}
}
