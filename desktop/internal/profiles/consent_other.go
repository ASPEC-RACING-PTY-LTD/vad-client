//go:build !windows

package profiles

func protect(pt, entropy []byte) ([]byte, error)     { return nil, ErrUnsupported }
func unprotect(blob, entropy []byte) ([]byte, error) { return nil, ErrUnsupported }
func currentUserSID() (string, error)                { return "", ErrUnsupported }
