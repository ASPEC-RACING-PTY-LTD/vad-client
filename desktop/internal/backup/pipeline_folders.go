package backup

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
)

// FolderResolver resolves a profile's known folders into the path tokens
// of backup-format §6 ({PROFILE}, {DOCUMENTS}, …, and the machine tokens).
// The pipeline uses it when the WalkSpec carries no Tokens; tests use a
// fake.
type FolderResolver interface {
	Tokens(ctx context.Context, prof guard.Profile) (snapshot.Tokens, error)
}

// shellFolderValues maps the per-user "User Shell Folders" registry value
// names to tokens. Downloads, Saved Games and LocalLow only exist as
// FOLDERID GUID names.
var shellFolderValues = map[string]string{
	"Desktop":                                snapshot.TokenDesktop,
	"Personal":                               snapshot.TokenDocuments,
	"My Pictures":                            snapshot.TokenPictures,
	"My Music":                               snapshot.TokenMusic,
	"My Video":                               snapshot.TokenVideos,
	"AppData":                                snapshot.TokenAppData,
	"Local AppData":                          snapshot.TokenLocalAppData,
	"{374DE290-123F-4565-9164-39C4925E467B}": snapshot.TokenDownloads,
	"{4C5C32FF-BB9D-43B0-B5B4-2D72E54EAAA4}": snapshot.TokenSavedGames,
	"{A520A1A4-1780-4FF6-BD18-167343C5AF16}": snapshot.TokenLocalLow,
}

// defaultFolders are the Windows defaults below the profile directory,
// used when the user's registry hive is not loaded (user logged off).
var defaultFolders = map[string]string{
	snapshot.TokenDesktop:      "Desktop",
	snapshot.TokenDocuments:    "Documents",
	snapshot.TokenDownloads:    "Downloads",
	snapshot.TokenPictures:     "Pictures",
	snapshot.TokenMusic:        "Music",
	snapshot.TokenVideos:       "Videos",
	snapshot.TokenSavedGames:   "Saved Games",
	snapshot.TokenAppData:      `AppData\Roaming`,
	snapshot.TokenLocalAppData: `AppData\Local`,
	snapshot.TokenLocalLow:     `AppData\LocalLow`,
}

// profileTokens builds the token map of a profile rooted at root. values
// are raw "User Shell Folders" values (may be nil); expand expands the
// remaining system variables (%SystemDrive% …) and may be nil. machine
// adds the machine tokens. Relative or unexpandable values fall back to
// the default below root.
func profileTokens(root string, values map[string]string, expand func(string) string, machine snapshot.Tokens) snapshot.Tokens {
	root = filepath.Clean(root)
	t := snapshot.Tokens{snapshot.TokenProfile: root}
	for tok, rel := range defaultFolders {
		t[tok] = filepath.Join(root, rel)
	}
	for name, raw := range values {
		tok, ok := shellFolderValues[canonicalValueName(name)]
		if !ok || raw == "" {
			continue
		}
		v := replaceVarFold(raw, "%USERPROFILE%", root)
		if expand != nil && strings.Contains(v, "%") {
			v = expand(v)
		}
		if strings.Contains(v, "%") || !isAbsWindows(v) {
			continue
		}
		t[tok] = filepath.Clean(v)
	}
	for k, v := range machine {
		if v != "" {
			t[k] = v
		}
	}
	return t
}

func canonicalValueName(n string) string {
	for k := range shellFolderValues {
		if strings.EqualFold(k, n) {
			return k
		}
	}
	return n
}

func replaceVarFold(s, v, with string) string {
	for {
		i := strings.Index(strings.ToUpper(s), strings.ToUpper(v))
		if i < 0 {
			return s
		}
		s = s[:i] + with + s[i+len(v):]
	}
}

// isAbsWindows accepts drive-absolute (C:\…) and UNC (\\server\share)
// paths on every platform.
func isAbsWindows(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z'))
}
