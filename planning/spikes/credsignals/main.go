//go:build windows

// Command credsignals is a read-only spike (P0-S4). It prints the
// unprivileged credential-change signals VaultDock can observe for the
// current user: SAM password age, SID/profile mapping and a DPAPI
// round-trip of an in-memory consent-like blob. It changes nothing and
// never handles passwords.
package main

import (
	"bytes"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// userInfo1 mirrors USER_INFO_1 (lmaccess.h).
type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32 // seconds since the password was last set
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

// userInfo24 mirrors USER_INFO_24: the Microsoft-account (internet
// identity) link of a local account.
type userInfo24 struct {
	InternetIdentity uint32 // BOOL
	Flags            uint32
	ProviderName     *uint16
	PrincipalName    *uint16
	UserSid          *windows.SID
}

const ufAccountDisable = 0x0002

func main() {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		fail("token user", err)
	}
	sid := tu.User.Sid
	acct, dom, _, err := sid.LookupAccount("")
	if err != nil {
		fail("lookup sid", err)
	}
	fmt.Printf("sid=%s account=%s/%s\n", sid, dom, acct)

	// Signal 1: SAM password age (NetUserGetInfo level 1, no privilege).
	var buf *byte
	name, _ := windows.UTF16PtrFromString(acct)
	if err := windows.NetUserGetInfo(nil, name, 1, &buf); err != nil {
		fmt.Printf("netusergetinfo: %v (not a local SAM account?)\n", err)
	} else {
		ui := (*userInfo1)(unsafe.Pointer(buf))
		age := time.Duration(ui.PasswordAge) * time.Second
		set := time.Now().Add(-age).UTC().Truncate(time.Second)
		fmt.Printf("password_age=%s password_last_set~=%s disabled=%v\n",
			age, set.Format(time.RFC3339), ui.Flags&ufAccountDisable != 0)
		windows.NetApiBufferFree(buf)
	}

	// MSA link (level 24). For MSA-linked accounts SAM keeps no
	// PasswordLastSet, so usri1_password_age reads 0 on every call.
	if err := windows.NetUserGetInfo(nil, name, 24, &buf); err != nil {
		fmt.Printf("level24: %v\n", err)
	} else {
		ui := (*userInfo24)(unsafe.Pointer(buf))
		fmt.Printf("internet_identity=%v provider=%s\n", ui.InternetIdentity != 0,
			windows.UTF16PtrToString(ui.ProviderName))
		windows.NetApiBufferFree(buf)
	}

	// Signal 5: profile path for the SID (ProfileList, readable by Users).
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid.String(),
		registry.QUERY_VALUE)
	if err != nil {
		fmt.Printf("profilelist: %v\n", err)
	} else {
		p, _, _ := k.GetStringValue("ProfileImagePath")
		k.Close()
		fmt.Printf("profile_path=%s\n", p)
	}

	// Signal 2: DPAPI user-scope round-trip (in memory only).
	blob := []byte(`{"spike":"consent-proof"}`)
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var prot, out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &prot); err != nil {
		fail("protect", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(prot.Data)))
	if err := windows.CryptUnprotectData(&prot, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		fmt.Printf("dpapi_unprotect=FAILED (%v) -> consent_unverifiable\n", err)
		return
	}
	got := unsafe.Slice(out.Data, out.Size)
	fmt.Printf("dpapi_roundtrip_ok=%v protected_len=%d\n", bytes.Equal(got, blob), prot.Size)
	windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
