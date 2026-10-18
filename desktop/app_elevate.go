package main

// Elevation is the UI process's own rights. Machine-wide actions (connecting
// to a server, settings, updates) need an elevated administrator; the UI is
// started without elevation, so it offers a restart as administrator.
type Elevation struct {
	Admin    bool `json:"admin"`    // member of Administrators (possibly filtered by UAC)
	Elevated bool `json:"elevated"` // running with the full administrator token
}

// Elevation reports this UI process's rights.
func (a *App) Elevation() Elevation { return currentElevation() }

// RestartAsAdmin starts this app elevated (the Windows UAC prompt) and closes
// this window. It returns an error when the prompt was declined.
func (a *App) RestartAsAdmin() error {
	if err := relaunchElevated(); err != nil {
		return err
	}
	a.Quit()
	return nil
}
