//go:build windows

package profiles

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func dataBlob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

// protect seals pt with user-scope DPAPI in the calling context.
func protect(pt, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(dataBlob(pt), windows.StringToUTF16Ptr("VaultDock consent"),
		dataBlob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("profiles: CryptProtectData: %w", err)
	}
	return takeBlob(&out), nil
}

// unprotect opens a user-scope blob in the current thread's security
// context (the impersonated session token in the service).
func unprotect(blob, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(dataBlob(blob), nil, dataBlob(entropy), 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, classifyDPAPI(err)
	}
	return takeBlob(&out), nil
}

func classifyDPAPI(err error) error {
	if errors.Is(err, syscall.Errno(windows.NTE_BAD_KEY_STATE)) {
		return fmt.Errorf("%w: %v", ErrDPAPIKeyUnavailable, err)
	}
	return fmt.Errorf("%w: %v", ErrConsentInvalid, err)
}

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("profiles: GetTokenUser: %w", err)
	}
	return u.User.Sid.String(), nil
}
