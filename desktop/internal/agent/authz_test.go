package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/ipc/caller"
)

func TestAuthorize(t *testing.T) {
	const userSID = "S-1-5-21-1004336348-1177238915-682003330-1001"
	user := caller.Caller{SID: userSID, SessionID: 1, PID: 42}
	adminUnelevated := caller.Caller{SID: userSID, SessionID: 1, Admin: true}
	adminElevated := caller.Caller{SID: userSID, SessionID: 1, Admin: true, Elevated: true}
	elevatedNotAdmin := caller.Caller{SID: userSID, SessionID: 1, Elevated: true}
	entra := caller.Caller{SID: "S-1-12-1-1234567890-1234567890-1234567890-1234567890", SessionID: 2}
	system := caller.Caller{SID: "S-1-5-18", System: true}

	tests := []struct {
		name   string
		c      caller.Caller
		method string
		allow  bool
	}{
		{"user reads status", user, "Bootstrap", true},
		{"user snapshot", user, "Snapshot", true},
		{"user subscribes", user, "Subscribe", true},
		{"user unsubscribes", user, "Unsubscribe", true},
		{"user saves own config", user, "SaveConfig", true},
		{"user syncs now", user, "SyncNow", true},
		{"user starts restore", user, "StartRestore", true},
		{"user gives consent", user, "GiveConsent", true},
		{"entra user reads", entra, "ListHistory", true},
		{"entra user saves config", entra, "SaveConfig", true},
		{"user connect denied", user, "Connect", false},
		{"user disconnect denied", user, "Disconnect", false},
		{"user probe denied", user, "ProbeServer", false},
		{"user settings denied", user, "SaveSettings", false},
		{"user pause denied", user, "SetPaused", false},
		{"user install update denied", user, "InstallUpdate", false},
		{"user set mode denied", user, "SetMode", false},
		{"unelevated admin connect denied", adminUnelevated, "Connect", false},
		{"unelevated admin reads", adminUnelevated, "GetSettings", true},
		{"elevated non-admin connect denied", elevatedNotAdmin, "Connect", false},
		{"elevated admin connect", adminElevated, "Connect", true},
		{"elevated admin install update", adminElevated, "InstallUpdate", true},
		{"elevated admin set mode", adminElevated, "SetMode", true},
		{"user reads key status", user, "KeyStatus", true},
		{"user reads readiness", user, "Readiness", true},
		{"user lists runs", user, "ListBackupRuns", true},
		{"user runs own backup", user, "RunBackup", true},
		{"user retries skipped", user, "RetrySkipped", true},
		{"user init repository denied", user, "InitRepository", false},
		{"user confirm identity denied", user, "ConfirmServerIdentity", false},
		{"user confirm recovery key denied", user, "ConfirmRecoveryKey", false},
		{"unelevated admin init repository denied", adminUnelevated, "InitRepository", false},
		{"elevated admin init repository", adminElevated, "InitRepository", true},
		{"elevated admin confirm identity", adminElevated, "ConfirmServerIdentity", true},
		{"system connect", system, "Connect", true},
		{"system reads", system, "Bootstrap", true},
		{"system flag without system SID", caller.Caller{SID: userSID, System: true}, "Connect", false},
		{"system SID without flag", caller.Caller{SID: "S-1-5-18"}, "Bootstrap", false},
		{"anonymous denied", caller.Caller{SID: "S-1-5-7"}, "Bootstrap", false},
		{"anonymous admin flags denied", caller.Caller{SID: "S-1-5-7", Admin: true, Elevated: true}, "Connect", false},
		{"network SID denied", caller.Caller{SID: "S-1-5-2"}, "Bootstrap", false},
		{"empty SID denied", caller.Caller{}, "Bootstrap", false},
		{"null SID denied", caller.Caller{SID: "S-1-0-0"}, "Bootstrap", false},
		{"world SID denied", caller.Caller{SID: "S-1-1-0"}, "Bootstrap", false},
		{"local service denied", caller.Caller{SID: "S-1-5-19"}, "Bootstrap", false},
		{"network service denied", caller.Caller{SID: "S-1-5-20", Admin: true, Elevated: true}, "Connect", false},
		{"virtual service account denied", caller.Caller{SID: "S-1-5-80-123-456"}, "Bootstrap", false},
		{"malformed SID denied", caller.Caller{SID: "S-1-5-21-abc"}, "Bootstrap", false},
		{"truncated SID denied", caller.Caller{SID: "S-1-5-21-"}, "Bootstrap", false},
		{"unknown method denied", adminElevated, "DropAllTables", false},
		{"unknown method denied for system", system, "DropAllTables", false},
		{"empty method denied", user, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Authorize(context.Background(), tt.c, tt.method)
			if tt.allow {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want forbidden, got allowed")
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeForbidden {
				t.Fatalf("want *Error code %q, got %#v", CodeForbidden, err)
			}
			if !errors.Is(err, ErrForbidden) {
				t.Fatal("errors.Is(err, ErrForbidden) = false")
			}
			if got := AsError(err); got.Code != CodeForbidden {
				t.Fatalf("AsError code = %q", got.Code)
			}
		})
	}
}

// Every agent.API method must have an explicit class, so a new method is
// never silently denied (or silently allowed) by the IPC policy.
func TestEveryAPIMethodClassified(t *testing.T) {
	typ := reflect.TypeOf((*API)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i).Name
		if MethodAccess(m) == AccessDenied {
			t.Errorf("API method %s has no access class in authz.go", m)
		}
	}
}

func TestAccessString(t *testing.T) {
	for a, want := range map[Access]string{AccessDenied: "denied", AccessRead: "read", AccessUser: "user", AccessAdmin: "admin"} {
		if a.String() != want {
			t.Errorf("%d.String() = %q, want %q", a, a.String(), want)
		}
	}
}
