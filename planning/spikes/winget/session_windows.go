//go:build windows

package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LaunchInUserSession is the call sequence the agent (LocalSystem with
// SeTcbPrivilege) would use to start vd-reinstall.exe in the interactive
// session of the user who consented. It never elevates: if the session token
// is already elevated (UAC off, or the built-in Administrator) it refuses, so
// elevation can only come from winget's or the installer's own UAC prompt.
//
// expectSID is the SID of the pipe caller that gave consent. The session user
// must match it, so a fast user switch between consent and launch cannot
// redirect the batch to another user.
func LaunchInUserSession(sessionID uint32, expectSID *windows.SID, exe string, args []string) (*windows.ProcessInformation, error) {
	var tok windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &tok); err != nil {
		return nil, fmt.Errorf("WTSQueryUserToken(%d): %w", sessionID, err)
	}
	defer tok.Close()

	tu, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("token user: %w", err)
	}
	if !windows.EqualSid(tu.User.Sid, expectSID) {
		return nil, errors.New("session user does not match consenting user")
	}
	if tok.IsElevated() {
		return nil, errors.New("session token is elevated; refusing silent elevation")
	}

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, tok, false); err != nil {
		return nil, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	profile, err := tok.GetUserProfileDirectory()
	if err != nil {
		return nil, fmt.Errorf("profile dir: %w", err)
	}

	exe16, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	cmd16, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return nil, err
	}
	dir16, err := windows.UTF16PtrFromString(profile)
	if err != nil {
		return nil, err
	}
	desk16, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return nil, err
	}

	si := windows.StartupInfo{Desktop: desk16}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	const flags = windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	if err := windows.CreateProcessAsUser(tok, exe16, cmd16, nil, nil, false, flags, env, dir16, &si, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	return &pi, nil
}
