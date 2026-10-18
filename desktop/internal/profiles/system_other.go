//go:build !windows

package profiles

// OSSystem is the platform System; on non-Windows builds it reports
// ErrUnsupported.
type OSSystem struct{}

// ActiveConsoleUser implements System.
func (OSSystem) ActiveConsoleUser() (ActiveUser, error) { return ActiveUser{}, ErrUnsupported }

// LocalProfiles implements System.
func (OSSystem) LocalProfiles() ([]LocalProfile, error) { return nil, ErrUnsupported }
