//go:build windows

package backup

import (
	"context"
	"errors"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const userShellFolders = `\Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`

// OSFolders resolves known folders from the profile's loaded registry hive
// (HKU\<SID>\…\User Shell Folders, which holds folder redirection), with
// %USERPROFILE% taken as prof.Root. When the hive is not loaded (the user
// is logged off and no S4U logon loaded it) the Windows defaults below
// prof.Root are used. Machine tokens come from the known-folder API.
type OSFolders struct{}

// Tokens implements FolderResolver.
func (OSFolders) Tokens(_ context.Context, prof guard.Profile) (snapshot.Tokens, error) {
	if err := tokens.ValidateSID(prof.SID); err != nil {
		return nil, err
	}
	if prof.Root == "" {
		return nil, errors.New("backup: profile directory unknown")
	}
	values := map[string]string{}
	if k, err := registry.OpenKey(registry.USERS, prof.SID+userShellFolders, registry.QUERY_VALUE); err == nil {
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			if v, _, err := k.GetStringValue(n); err == nil {
				values[n] = v
			}
		}
		k.Close()
	}
	expand := func(s string) string {
		v, err := registry.ExpandString(s)
		if err != nil {
			return s
		}
		return v
	}
	return profileTokens(prof.Root, values, expand, machineTokens()), nil
}

func machineTokens() snapshot.Tokens {
	t := snapshot.Tokens{}
	for tok, id := range map[string]*windows.KNOWNFOLDERID{
		snapshot.TokenProgramData:     windows.FOLDERID_ProgramData,
		snapshot.TokenProgramFiles:    windows.FOLDERID_ProgramFiles,
		snapshot.TokenProgramFilesX86: windows.FOLDERID_ProgramFilesX86,
	} {
		if p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY); err == nil && p != "" {
			t[tok] = p
		}
	}
	return t
}
