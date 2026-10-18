//go:build windows

package profiles

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
)

const profileListKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList`

// ProfileList State flags (PI_*) that mark a temporary or backup profile.
const (
	profileStateTempAssigned = 0x0004
	profileStateBackup       = 0x0800
)

var (
	modwtsapi32                     = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = modwtsapi32.NewProc("WTSQuerySessionInformationW")
)

const (
	wtsUserName   = 5
	wtsDomainName = 7
	noSession     = 0xFFFFFFFF
)

// OSSystem is the Windows System: unprivileged WTS queries and a read-only
// ProfileList walk. Works as SYSTEM and as a normal user.
type OSSystem struct{}

func wtsString(session uint32, class uintptr) (string, error) {
	var buf *uint16
	var n uint32
	r, _, e := procWTSQuerySessionInformationW.Call(0, uintptr(session), class,
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "", e
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf), nil
}

// ActiveConsoleUser implements System: WTSGetActiveConsoleSessionId, then
// the session's user and domain names, then LookupAccountName for the SID
// (no token needed, so it works unprivileged).
func (OSSystem) ActiveConsoleUser() (ActiveUser, error) {
	id := windows.WTSGetActiveConsoleSessionId()
	if id == noSession {
		return ActiveUser{}, ErrNoActiveUser
	}
	user, err := wtsString(id, wtsUserName)
	if err != nil {
		return ActiveUser{}, fmt.Errorf("profiles: WTSQuerySessionInformation: %w", err)
	}
	if user == "" {
		return ActiveUser{}, ErrNoActiveUser
	}
	dom, err := wtsString(id, wtsDomainName)
	if err != nil {
		return ActiveUser{}, fmt.Errorf("profiles: WTSQuerySessionInformation: %w", err)
	}
	name := user
	if dom != "" {
		name = dom + `\` + user
	}
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return ActiveUser{}, fmt.Errorf("profiles: LookupAccountName: %w", err)
	}
	return ActiveUser{SessionID: id, SID: sid.String(), Username: name}, nil
}

// LocalProfiles implements System.
func (OSSystem) LocalProfiles() ([]LocalProfile, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, profileListKey, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("profiles: open ProfileList: %w", err)
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("profiles: enumerate ProfileList: %w", err)
	}
	var out []LocalProfile
	seen := map[string]int{}
	for _, name := range names {
		sid, bak := strings.CutSuffix(name, ".bak")
		if !tokens.IsUserSID(sid) {
			continue
		}
		p, err := readProfile(k, name)
		if err != nil {
			continue // unreadable entry: skip, never fail the whole walk
		}
		p.SID = sid
		p.Temporary = p.Temporary || bak
		p.Username = accountName(sid)
		if i, ok := seen[sid]; ok {
			// Both <SID> and <SID>.bak exist: the profile is temporary.
			out[i].Temporary = true
			continue
		}
		seen[sid] = len(out)
		out = append(out, p)
	}
	return out, nil
}

func readProfile(parent registry.Key, name string) (LocalProfile, error) {
	k, err := registry.OpenKey(parent, name, registry.QUERY_VALUE)
	if err != nil {
		return LocalProfile{}, err
	}
	defer k.Close()
	path, _, err := k.GetStringValue("ProfileImagePath")
	if err != nil {
		return LocalProfile{}, err
	}
	if exp, err := registry.ExpandString(path); err == nil {
		path = exp
	}
	var p LocalProfile
	p.ProfilePath = path
	if st, _, err := k.GetIntegerValue("State"); err == nil && st&(profileStateTempAssigned|profileStateBackup) != 0 {
		p.Temporary = true
	}
	return p, nil
}

func accountName(sidStr string) string {
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return ""
	}
	acct, dom, _, err := sid.LookupAccount("")
	if err != nil {
		return ""
	}
	if dom == "" {
		return acct
	}
	return dom + `\` + acct
}
