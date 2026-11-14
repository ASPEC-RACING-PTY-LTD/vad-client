package main

import (
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
)

// App lock bindings: the PIN goes to the agent, which derives the key;
// Windows Hello runs here, in the signed-in user's process, and only the
// unlocked lock key reaches the agent.

// AppLock describes the lock for the UI.
type AppLock struct {
	Enabled        bool  `json:"enabled"`
	Locked         bool  `json:"locked"`
	Hello          bool  `json:"hello"`           // Windows Hello unlock is set up
	HelloSupported bool  `json:"hello_supported"` // this PC and account can use Windows Hello
	RetryAfter     int64 `json:"retry_after,omitempty"`
}

// GetAppLock returns the lock state.
func (a *App) GetAppLock() (AppLock, error) {
	st, err := a.api.LockStatus(a.bg)
	if err != nil {
		return AppLock{}, uiError(err)
	}
	return AppLock{Enabled: st.Enabled, Locked: st.Locked, Hello: st.Hello, HelloSupported: helloSupported(), RetryAfter: st.RetryAfter}, nil
}

// SetupAppLock sets the 6-digit PIN (current is needed to change it).
func (a *App) SetupAppLock(pin, current string) error {
	return uiError(a.api.SetupLock(a.bg, agent.SetupLockRequest{PIN: pin, CurrentPIN: current}))
}

// UnlockWithPIN unlocks with the PIN.
func (a *App) UnlockWithPIN(pin string) error {
	return uiError(a.api.Unlock(a.bg, agent.UnlockRequest{PIN: pin}))
}

// UnlockWithHello shows Windows Hello and unlocks with the key it releases.
func (a *App) UnlockWithHello() error {
	st, err := a.api.LockStatus(a.bg)
	if err != nil {
		return uiError(err)
	}
	secret, err := helloDecrypt(st.HelloKey, st.HelloBox)
	if err != nil {
		return err
	}
	defer clear(secret)
	return uiError(a.api.Unlock(a.bg, agent.UnlockRequest{Secret: secret}))
}

// EnableHello creates the Windows Hello unlock key (showing Windows Hello)
// and gives the agent its public key. The app must be unlocked.
func (a *App) EnableHello() error {
	name, mod, exp, err := helloCreate()
	if err != nil {
		return err
	}
	if err := a.api.SetHello(a.bg, agent.HelloRequest{KeyName: name, Modulus: mod, Exponent: exp}); err != nil {
		helloDelete(name)
		return uiError(err)
	}
	return nil
}

// DisableHello removes the Windows Hello unlock; the PIN keeps working.
func (a *App) DisableHello() error {
	st, err := a.api.LockStatus(a.bg)
	if err != nil {
		return uiError(err)
	}
	if err := a.api.SetHello(a.bg, agent.HelloRequest{}); err != nil {
		return uiError(err)
	}
	if st.HelloKey != "" {
		helloDelete(st.HelloKey)
	}
	return nil
}

// LockNow locks the app.
func (a *App) LockNow() error { return uiError(a.api.Lock(a.bg)) }
