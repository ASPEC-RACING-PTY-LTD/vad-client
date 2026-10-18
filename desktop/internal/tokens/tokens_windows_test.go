//go:build windows

package tokens

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func currentSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

func TestDefaultIsWindowsProvider(t *testing.T) {
	if _, ok := Default.(*WindowsProvider); !ok {
		t.Fatalf("Default = %T", Default)
	}
}

// TestNotSystem: an unprivileged (non-LocalSystem) process gets
// ErrInsufficientPrivilege before any session or LSA call.
func TestNotSystem(t *testing.T) {
	sys, err := processIsSystem()
	if err != nil {
		t.Fatal(err)
	}
	if sys {
		t.Skip("running as LocalSystem")
	}
	sid := currentSID(t)
	p := NewProvider()
	for name, f := range map[string]func(string) (Token, error){"ForProfile": p.ForProfile, "SessionOnly": p.SessionOnly} {
		tok, err := f(sid)
		if tok != nil || !errors.Is(err, ErrInsufficientPrivilege) {
			t.Errorf("%s = %v, %v; want ErrInsufficientPrivilege", name, tok, err)
		}
	}
	if _, err := p.ForProfile("not-a-sid"); !errors.Is(err, ErrInvalidSID) {
		t.Errorf("invalid SID: %v", err)
	}
}

func TestNotSystemInjected(t *testing.T) {
	p := &WindowsProvider{isSystem: func() (bool, error) { return false, nil }}
	if _, err := p.ForProfile("S-1-5-21-1-2-3-1001"); !errors.Is(err, ErrInsufficientPrivilege) {
		t.Fatalf("err = %v", err)
	}
	p.isSystem = func() (bool, error) { return false, errors.New("no token") }
	if _, err := p.SessionOnly("S-1-5-21-1-2-3-1001"); !errors.Is(err, ErrInsufficientPrivilege) {
		t.Fatalf("err = %v", err)
	}
}

func TestClosedToken(t *testing.T) {
	tok := &winToken{sid: "S-1-5-21-1-2-3-1001", source: SourceS4U}
	if err := tok.Impersonate(func() error { return nil }); !errors.Is(err, errClosed) {
		t.Errorf("Impersonate = %v", err)
	}
	if _, err := tok.PrimaryDup(); !errors.Is(err, errClosed) {
		t.Errorf("PrimaryDup = %v", err)
	}
	if err := tok.Close(); err != nil {
		t.Error(err)
	}
	if err := tok.Close(); err != nil {
		t.Error(err)
	}
	if _, ok := tok.SessionID(); ok {
		t.Error("s4u has no session")
	}
	var _ Token = tok
	var _ PrimaryToken = tok
	var _ SecurityChecker = tok
}
