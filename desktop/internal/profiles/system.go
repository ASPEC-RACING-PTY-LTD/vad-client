package profiles

// LocalProfile is a profile found in the ProfileList registry key.
type LocalProfile struct {
	SID         string
	Username    string // DOMAIN\name; empty when the account cannot be resolved
	ProfilePath string // ProfileImagePath, environment variables expanded
	Temporary   bool   // a ".bak" duplicate key or a temporary/backup State flag
}

// ActiveUser is the user signed in at the physical console.
type ActiveUser struct {
	SessionID uint32
	SID       string
	Username  string // DOMAIN\name
}

// System is the read-only OS view the Manager uses; Windows implements it
// with WTS and the registry (system_windows.go), tests inject a fake.
type System interface {
	// ActiveConsoleUser returns the console session's user or
	// ErrNoActiveUser.
	ActiveConsoleUser() (ActiveUser, error)
	// LocalProfiles lists user profiles (S-1-5-21-…, S-1-12-1-…) from
	// HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList. It
	// never loads hives.
	LocalProfiles() ([]LocalProfile, error)
}
