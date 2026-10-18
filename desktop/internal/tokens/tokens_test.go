package tokens

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSID(t *testing.T) {
	tests := []struct {
		sid string
		ok  bool
	}{
		{"S-1-5-21-1004336348-1177238915-682003330-1001", true},
		{"S-1-5-18", true},
		{"S-1-12-1-1234-5678-9012-3456", true},
		{"S-1-123456789012-5", true}, // authority may have up to 14 digits
		{"", false},
		{"S-1", false},
		{"s-1-5-18", false},
		{"S-2-5-18", false},
		{"S-1-5--18", false},
		{"S-1-5-x", false},
		{"S-1-5-18 ", false},
		{"S-1-5-12345678901", false},
		{"S-1-123456789012345-5", false},
		{"S-1-5" + strings.Repeat("-1", 16), false},
	}
	for _, tt := range tests {
		err := ValidateSID(tt.sid)
		if (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrInvalidSID)) {
			t.Errorf("ValidateSID(%q) = %v, want ok=%v", tt.sid, err, tt.ok)
		}
	}
}

func TestIsUserSID(t *testing.T) {
	for sid, want := range map[string]bool{
		"S-1-5-21-1-2-3-1001":  true,
		"S-1-12-1-11-22-33-44": true,
		"S-1-5-18":             false,
		"S-1-5-32-544":         false,
		"S-1-5-21-x":           false,
		"S-1-5-80-1-2-3-4-5":   false,
		"S-1-5-21-1-2-3-1001 ": false,
	} {
		if got := IsUserSID(sid); got != want {
			t.Errorf("IsUserSID(%q) = %v, want %v", sid, got, want)
		}
	}
}

func TestUnsupportedProvider(t *testing.T) {
	var p Provider = Unsupported{}
	for _, f := range []func(string) (Token, error){p.ForProfile, p.SessionOnly} {
		if tok, err := f("S-1-5-21-1-2-3-1001"); tok != nil || !errors.Is(err, ErrUnsupported) {
			t.Errorf("valid SID: %v, %v", tok, err)
		}
		if tok, err := f("bogus"); tok != nil || !errors.Is(err, ErrInvalidSID) {
			t.Errorf("invalid SID: %v, %v", tok, err)
		}
	}
}

func TestCaps(t *testing.T) {
	if c := CapsFor(SourceSession); !c.Files || !c.DPAPI || !c.EFS {
		t.Errorf("session caps = %+v", c)
	}
	if c := CapsFor(SourceS4U); !c.Files || c.DPAPI || c.EFS || c.Network {
		t.Errorf("s4u caps = %+v", c)
	}
	if c := CapsFor(0); c != (Caps{}) {
		t.Errorf("zero caps = %+v", c)
	}
	if SourceSession.String() != "session" || SourceS4U.String() != "s4u" || Source(9).String() != "unknown" {
		t.Error("Source.String")
	}
}

func TestNTStatusMapping(t *testing.T) {
	tests := []struct {
		st   uint32
		want error
	}{
		{statusPortConnectionRefused, ErrInsufficientPrivilege},
		{statusPrivilegeNotHeld, ErrInsufficientPrivilege},
		{statusAccessDenied, ErrInsufficientPrivilege},
		{statusNoSuchUser, ErrAccountNotFound},
		{statusNoLogonServers, ErrS4UUnavailable},
		{statusAccountDisabled, ErrS4UUnavailable},
		{statusLogonTypeNotGranted, ErrS4UUnavailable},
		{statusNoTrustSamAccount, ErrS4UUnavailable},
		{0xC0000001, ErrS4UUnavailable},
	}
	for _, tt := range tests {
		err := ntStatusError("LsaLogonUser", "S-1-5-21-1-2-3-1001", tt.st, 0xC000006D)
		if !errors.Is(err, tt.want) {
			t.Errorf("0x%08X: %v, want %v", tt.st, err, tt.want)
		}
		var te *Error
		if !errors.As(err, &te) || te.Op != "LsaLogonUser" || !strings.Contains(err.Error(), "substatus 0xC000006D") {
			t.Errorf("0x%08X: bad error %q", tt.st, err)
		}
	}
}

func TestWin32Mapping(t *testing.T) {
	fallback := errors.New("fallback")
	tests := []struct {
		code uint32
		want error
	}{
		{errorPrivilegeNotHeld, ErrInsufficientPrivilege},
		{errorNotAllAssigned, ErrInsufficientPrivilege},
		{errorAccessDenied, ErrInsufficientPrivilege},
		{errorNoToken, ErrNotLoggedOn},
		{errorNoneMapped, ErrAccountNotFound},
		{errorTrustedRelFailure, ErrS4UUnavailable},
		{errorNoLogonServers, ErrS4UUnavailable},
		{87, fallback},
	}
	for _, tt := range tests {
		if got := win32Sentinel(tt.code, fallback); got != tt.want {
			t.Errorf("%d: %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestPackageFor(t *testing.T) {
	tests := []struct {
		domain, host string
		pkg          string
		err          error
	}{
		{"ALPHA", "alpha", PackageMSV, nil},
		{"AzureAD", "ALPHA", "", ErrNeedsLogon},
		{"azuread", "ALPHA", "", ErrNeedsLogon},
		{"CORP", "ALPHA", PackageNegotiate, nil},
		{"", "ALPHA", "", ErrS4UUnavailable},
	}
	for _, tt := range tests {
		pkg, err := PackageFor(tt.domain, tt.host)
		if pkg != tt.pkg || !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
			t.Errorf("PackageFor(%q,%q) = %q, %v", tt.domain, tt.host, pkg, err)
		}
	}
}

func TestErrorFormat(t *testing.T) {
	cause := errors.New("boom")
	err := newErr("WTSQueryUserToken", "S-1-5-18", ErrInsufficientPrivilege, cause)
	if !errors.Is(err, ErrInsufficientPrivilege) || !errors.Is(err, cause) {
		t.Fatal("Is")
	}
	if got := err.Error(); !strings.Contains(got, "WTSQueryUserToken: boom") || !strings.Contains(got, "[S-1-5-18]") {
		t.Errorf("Error() = %q", got)
	}
	if got := newErr("", "", ErrNotLoggedOn, nil).Error(); got != ErrNotLoggedOn.Error() {
		t.Errorf("Error() = %q", got)
	}
}
