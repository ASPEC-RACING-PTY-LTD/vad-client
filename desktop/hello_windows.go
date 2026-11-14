//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Hello unlock: an RSA key in the signed-in user's Windows Hello
// container (Microsoft Passport key storage provider, TPM-backed where
// available). Using it for decryption always shows the Windows Hello prompt
// (face, fingerprint or Hello PIN). The agent only ever gets its public key.

var (
	ncrypt                       = windows.NewLazySystemDLL("ncrypt.dll")
	procNCryptOpenStorageProvide = ncrypt.NewProc("NCryptOpenStorageProvider")
	procNCryptCreatePersistedKey = ncrypt.NewProc("NCryptCreatePersistedKey")
	procNCryptOpenKey            = ncrypt.NewProc("NCryptOpenKey")
	procNCryptSetProperty        = ncrypt.NewProc("NCryptSetProperty")
	procNCryptFinalizeKey        = ncrypt.NewProc("NCryptFinalizeKey")
	procNCryptExportKey          = ncrypt.NewProc("NCryptExportKey")
	procNCryptDecrypt            = ncrypt.NewProc("NCryptDecrypt")
	procNCryptDeleteKey          = ncrypt.NewProc("NCryptDeleteKey")
	procNCryptFreeObject         = ncrypt.NewProc("NCryptFreeObject")
	procFindWindowW              = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW")
)

const (
	helloProvider       = "Microsoft Passport Key Storage Provider"
	ncryptOverwriteKey  = 0x80
	ncryptPadPKCS1      = 0x2
	ncryptAllowDecrypt  = 0x1
	ncryptAllowSigning  = 0x2
	ngcAuthMandatory    = 0x1
	nteUserCancelled    = 0x80090036
	nteNotFound         = 0x80090011
	nteBadKeyset        = 0x80090016
	bcryptRSAPublicBlob = "RSAPUBLICBLOB"
)

func ncryptErr(op string, r uintptr) error {
	switch uint32(r) {
	case nteUserCancelled:
		return errors.New("Windows Hello was cancelled")
	case nteNotFound, nteBadKeyset:
		return errors.New("the Windows Hello key for VaultDock was not found; set up Windows Hello again in Settings")
	}
	return fmt.Errorf("%s failed (0x%08x); check that Windows Hello is set up for this account", op, uint32(r))
}

func helloOpenProvider() (uintptr, error) {
	if err := ncrypt.Load(); err != nil {
		return 0, err
	}
	name, _ := windows.UTF16PtrFromString(helloProvider)
	var prov uintptr
	if r, _, _ := procNCryptOpenStorageProvide.Call(uintptr(unsafe.Pointer(&prov)), uintptr(unsafe.Pointer(name)), 0); r != 0 {
		return 0, ncryptErr("Opening Windows Hello", r)
	}
	return prov, nil
}

func helloSupported() bool {
	prov, err := helloOpenProvider()
	if err != nil {
		return false
	}
	procNCryptFreeObject.Call(prov)
	return true
}

// helloKeyName is "<user SID>//VaultDock/AppLock/<name>", the form the
// Passport provider expects.
func helloKeyName() (string, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String() + "//VaultDock/AppLock/unlock", nil
}

func setProp(h uintptr, name string, val []byte) error {
	n, _ := windows.UTF16PtrFromString(name)
	if r, _, _ := procNCryptSetProperty.Call(h, uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&val[0])), uintptr(len(val)), 0); r != 0 {
		return ncryptErr("Setting "+name, r)
	}
	return nil
}

func dword(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func windowHandle() uintptr {
	title, _ := windows.UTF16PtrFromString("VaultDock")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	return h
}

// promptProps makes the Windows Hello prompt belong to the VaultDock window
// and say why it is shown.
func promptProps(key uintptr, why string) {
	if hwnd := windowHandle(); hwnd != 0 {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, uint64(hwnd))
		_ = setProp(key, "HWND Handle", b)
	}
	msg := windows.StringToUTF16(why)
	_ = setProp(key, "Use Context", unsafe.Slice((*byte)(unsafe.Pointer(&msg[0])), len(msg)*2))
}

// helloCreate creates (or replaces) the unlock key, showing Windows Hello,
// and returns its name and RSA public key.
func helloCreate() (name string, modulus, exponent []byte, err error) {
	prov, err := helloOpenProvider()
	if err != nil {
		return "", nil, nil, err
	}
	defer procNCryptFreeObject.Call(prov)
	if name, err = helloKeyName(); err != nil {
		return "", nil, nil, err
	}
	alg, _ := windows.UTF16PtrFromString("RSA")
	kn, _ := windows.UTF16PtrFromString(name)
	var key uintptr
	if r, _, _ := procNCryptCreatePersistedKey.Call(prov, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(alg)), uintptr(unsafe.Pointer(kn)), 0, ncryptOverwriteKey); r != 0 {
		return "", nil, nil, ncryptErr("Creating the Windows Hello key", r)
	}
	defer procNCryptFreeObject.Call(key)
	if err := setProp(key, "Length", dword(2048)); err != nil {
		return "", nil, nil, err
	}
	if err := setProp(key, "Key Usage", dword(ncryptAllowDecrypt|ncryptAllowSigning)); err != nil {
		return "", nil, nil, err
	}
	_ = setProp(key, "NgcCacheType", dword(ngcAuthMandatory))
	promptProps(key, "Use Windows Hello to unlock VaultDock")
	if r, _, _ := procNCryptFinalizeKey.Call(key, 0); r != 0 {
		return "", nil, nil, ncryptErr("Creating the Windows Hello key", r)
	}
	blob, err := exportPublic(key)
	if err != nil {
		return "", nil, nil, err
	}
	modulus, exponent, err = parseRSAPublicBlob(blob)
	return name, modulus, exponent, err
}

func exportPublic(key uintptr) ([]byte, error) {
	bt, _ := windows.UTF16PtrFromString(bcryptRSAPublicBlob)
	var size uint32
	if r, _, _ := procNCryptExportKey.Call(key, 0, uintptr(unsafe.Pointer(bt)), 0, 0, 0, uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return nil, ncryptErr("Reading the Windows Hello key", r)
	}
	buf := make([]byte, size)
	if r, _, _ := procNCryptExportKey.Call(key, 0, uintptr(unsafe.Pointer(bt)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return nil, ncryptErr("Reading the Windows Hello key", r)
	}
	return buf[:size], nil
}

// parseRSAPublicBlob reads a BCRYPT_RSAKEY_BLOB ("RSA1"): magic, bit
// length, exponent size, modulus size, two prime sizes, then the exponent
// and modulus (big-endian).
func parseRSAPublicBlob(b []byte) (modulus, exponent []byte, err error) {
	if len(b) < 24 || binary.LittleEndian.Uint32(b) != 0x31415352 {
		return nil, nil, errors.New("unexpected Windows Hello public key format")
	}
	ce := int(binary.LittleEndian.Uint32(b[8:]))
	cm := int(binary.LittleEndian.Uint32(b[12:]))
	if len(b) < 24+ce+cm {
		return nil, nil, errors.New("truncated Windows Hello public key")
	}
	return b[24+ce : 24+ce+cm], b[24 : 24+ce], nil
}

// helloDecrypt decrypts box with the unlock key, showing Windows Hello.
func helloDecrypt(name string, box []byte) ([]byte, error) {
	if len(box) == 0 {
		return nil, errors.New("Windows Hello is not set up for VaultDock")
	}
	prov, err := helloOpenProvider()
	if err != nil {
		return nil, err
	}
	defer procNCryptFreeObject.Call(prov)
	kn, _ := windows.UTF16PtrFromString(name)
	var key uintptr
	if r, _, _ := procNCryptOpenKey.Call(prov, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(kn)), 0, 0); r != 0 {
		return nil, ncryptErr("Opening the Windows Hello key", r)
	}
	defer procNCryptFreeObject.Call(key)
	promptProps(key, "Unlock VaultDock")
	var size uint32
	if r, _, _ := procNCryptDecrypt.Call(key, uintptr(unsafe.Pointer(&box[0])), uintptr(len(box)), 0, 0, 0, uintptr(unsafe.Pointer(&size)), ncryptPadPKCS1); r != 0 {
		return nil, ncryptErr("Windows Hello", r)
	}
	out := make([]byte, size)
	if r, _, _ := procNCryptDecrypt.Call(key, uintptr(unsafe.Pointer(&box[0])), uintptr(len(box)), 0, uintptr(unsafe.Pointer(&out[0])), uintptr(size), uintptr(unsafe.Pointer(&size)), ncryptPadPKCS1); r != 0 {
		return nil, ncryptErr("Windows Hello", r)
	}
	return out[:size], nil
}

// helloDelete removes the unlock key (best effort).
func helloDelete(name string) {
	prov, err := helloOpenProvider()
	if err != nil {
		return
	}
	defer procNCryptFreeObject.Call(prov)
	kn, _ := windows.UTF16PtrFromString(name)
	var key uintptr
	if r, _, _ := procNCryptOpenKey.Call(prov, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(kn)), 0, 0); r == 0 {
		procNCryptDeleteKey.Call(key, 0)
	}
}
